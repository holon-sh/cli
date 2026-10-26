package typegen

import (
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

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

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

// GeneratedSource reports dependency directories and marked client slots excluded from source snapshots.
func GeneratedSource(root, path string) bool {
	relative, _ := filepath.Rel(root, path)
	if slices.ContainsFunc(strings.Split(filepath.ToSlash(relative), "/"), func(part string) bool {
		return slices.Contains([]string{".git", "node_modules", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache"}, part)
	}) {
		return true
	}
	managed := filepath.Join(root, "holon")
	return pathInsideContext(path, managed) && generatedGo(managed) || pathInsideContext(path, filepath.Join(managed, "apps")) && generatedPython(managed)
}

// snapshotSource copies dir into destination (or only fingerprints it when destination is empty),
// honouring .dockerignore and skipping generated and dependency directories.
func snapshotSource(name, dir, destination string) (string, error) {
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
	matcher, err := sourceMatcher(root)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	err = filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		ignored, err := ignoredSource(root, path, matcher)
		if err != nil {
			return err
		}
		if ignored {
			if item.IsDir() && (GeneratedSource(root, path) || !matcher.Exclusions()) {
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
			if !pathInsideContext(resolved, root) {
				return fmt.Errorf("source symlink escapes application: %s", relative)
			}
			excluded, err := ignoredSource(root, resolved, matcher)
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
		if err := regularNoSymlink(filepath.Join(root, name), "server entry"); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		if entry != "" {
			return "", fmt.Errorf("keep exactly one server entry (found %s and %s)", entry, name)
		}
		entry = name
	}
	return entry, nil
}

func ignoredSource(root, path string, matcher *patternmatcher.PatternMatcher) (bool, error) {
	if path == filepath.Join(root, dockerignoreFile) {
		return false, nil
	}
	relative, err := filepath.Rel(root, path)
	if !filepath.IsLocal(relative) {
		return true, err
	}
	if relative == "." {
		return false, nil
	}
	if GeneratedSource(root, path) {
		return true, nil
	}
	return matcher.MatchesOrParentMatches(filepath.ToSlash(relative))
}
