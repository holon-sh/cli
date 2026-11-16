package typegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// SourceRules decide which files in an application directory are its source.
type SourceRules struct {
	root, top  string
	docker     *patternmatcher.PatternMatcher
	submodules []string
	patterns   map[string][]gitignore.Pattern
}

// ReadSourceRules reads the source rules of the application at root.
func ReadSourceRules(root string) (*SourceRules, error) {
	docker, err := sourceMatcher(root)
	if err != nil {
		return nil, err
	}
	s := &SourceRules{root: root, top: root, docker: docker, patterns: map[string][]gitignore.Pattern{}}
	for dir := root; filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			s.top = dir
			break
		}
	}
	data, err := os.ReadFile(filepath.Join(s.top, ".gitmodules"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	modules := config.New()
	if err := config.NewDecoder(bytes.NewReader(data)).Decode(modules); err != nil {
		return nil, err
	}
	for _, module := range modules.Section("submodule").Subsections {
		s.submodules = append(s.submodules, module.Option("path"))
	}
	return s, nil
}

// Ignored reports whether path is left out of the source, and whether everything below it is too.
func (s *SourceRules) Ignored(path string, dir bool) (ignored, below bool, err error) {
	relative, err := filepath.Rel(s.root, path)
	if !filepath.IsLocal(relative) {
		return true, true, err
	}
	if relative == "." || relative == dockerignoreFile {
		return false, false, nil
	}
	if slices.Contains(strings.Split(filepath.ToSlash(relative), "/"), ".git") || generatedSource(s.root, path) {
		return true, true, nil
	}
	if ignored, err := s.gitignored(path, dir); ignored || err != nil {
		return ignored, true, err
	}
	ignored, err = s.docker.MatchesOrParentMatches(filepath.ToSlash(relative))
	return ignored, ignored && !s.docker.Exclusions(), err
}

func (s *SourceRules) gitignored(path string, dir bool) (bool, error) {
	relative, _ := filepath.Rel(s.top, path)
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if _, err := os.Lstat(filepath.Join(path, ".git")); dir && err == nil && !slices.Contains(s.submodules, filepath.ToSlash(relative)) {
		return true, nil
	}
	var patterns []gitignore.Pattern
	for i := range parts {
		folder := filepath.Join(s.top, filepath.Join(parts[:i]...))
		if _, read := s.patterns[folder]; !read {
			data, err := os.ReadFile(filepath.Join(folder, ".gitignore"))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, err
			}
			for line := range strings.Lines(string(data)) {
				if line = strings.TrimRight(line, "\r\n"); strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
					s.patterns[folder] = append(s.patterns[folder], gitignore.ParsePattern(line, parts[:i]))
				}
			}
		}
		patterns = append(patterns, s.patterns[folder]...)
	}
	return gitignore.NewMatcher(patterns).Match(parts, dir), nil
}

func sourceMatcher(root string) (*patternmatcher.PatternMatcher, error) {
	file, err := os.Open(filepath.Join(root, dockerignoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		return patternmatcher.New(nil)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	patterns, err := ignorefile.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return patternmatcher.New(patterns)
}

func generatedSource(root, path string) bool {
	relative, _ := filepath.Rel(root, path)
	if slices.ContainsFunc(strings.Split(filepath.ToSlash(relative), "/"), func(part string) bool {
		return strings.HasPrefix(part, generatedTemp)
	}) {
		return true
	}
	return slices.ContainsFunc(targets, func(target string) bool {
		return lexicallyInside(path, SlotDirectory(root, target)) && generatedSlot(root, target)
	})
}

// SnapshotSource copies dir's source into destination, or only fingerprints it when destination is
// empty.
func SnapshotSource(name, dir, destination string) (string, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("build context for %s: %w", name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("build context for %s must be a real directory", name)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	rules, err := ReadSourceRules(root)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	err = filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		ignored, below, err := rules.Ignored(path, item.IsDir())
		if err != nil {
			return err
		}
		if ignored {
			if item.IsDir() && below {
				return filepath.SkipDir
			}
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		target := filepath.Join(destination, relative)
		info, err := item.Info()
		if err != nil {
			return err
		}
		if item.IsDir() {
			if destination == "" {
				return nil
			}
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		if destination != "" {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", filepath.ToSlash(relative), info.Mode(), info.Size())
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if !clientutil.PathInside(resolved, root) {
				return fmt.Errorf("source symlink escapes application: %s", relative)
			}
			linked, err := os.Stat(resolved)
			if err != nil {
				return err
			}
			excluded, _, err := rules.Ignored(resolved, linked.IsDir())
			if err != nil {
				return err
			}
			if excluded {
				return fmt.Errorf("source symlink targets an excluded file: %s", relative)
			}
			link, _ := filepath.Rel(filepath.Dir(path), resolved)
			_, _ = io.WriteString(hash, link)
			if destination == "" {
				return nil
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported source entry: %s", relative)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		if destination == "" {
			_, err := io.Copy(hash, input)
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
		return errors.Join(copyErr, output.Close(), os.Chtimes(target, info.ModTime(), info.ModTime()))
	})
	if err != nil {
		return "", err
	}
	if destination == "" {
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	err = os.Remove(filepath.Join(destination, dockerignoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return hex.EncodeToString(hash.Sum(nil)), err
}

// entryFiles are the server entry locations, relative to the application root.
var entryFiles = []string{"server.py", "src/server.py", "server.ts", "src/server.ts", "server.js", "src/server.js", "server.go"}

// SourceEntry returns the one server entry relative to root, or "" when there is none.
// More than one entry is an error.
func SourceEntry(root string) (string, error) {
	var entry string
	for _, name := range entryFiles {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("server entry must be a regular file: %s", path)
		}
		if entry != "" {
			return "", fmt.Errorf("keep exactly one server entry (found %s and %s)", entry, name)
		}
		entry = name
	}
	return entry, nil
}
