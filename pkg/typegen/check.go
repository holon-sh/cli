package typegen

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/holon-sh/cli/internal/commandenv"
)

// Check type-checks each consumer against freshly generated clients, exclusively in temporary copies.
func Check(ctx context.Context, consumers []Consumer, options Options) error {
	o := options.defaults()
	prepared, err := Prepare(ctx, consumers, o)
	if err != nil {
		return err
	}
	defer prepared.Close()
	if err = prepared.TypeCheck(ctx); err != nil {
		return err
	}
	fmt.Fprintln(o.Stdout, "Typed contracts check passed.")
	return nil
}

// TypeCheck copies each consumer, writes its generated clients into the copy beside the consumer's
// installed dependencies, and runs the language's type checker there. Consumers and installed slots
// are never modified. The go target is not checked.
func (p *Prepared) TypeCheck(ctx context.Context) error {
	for i, c := range p.consumers {
		if err := typeCheck(ctx, c, p, filepath.Join(p.root, "check", strconv.Itoa(i))); err != nil {
			return err
		}
	}
	return nil
}

// CheckConfig is the runner's check configuration ({entry, sources, check}) for the consumer's own
// server, for a type check inside an image build against the dependencies installed there. Paths are
// relative to the consumer directory. It reports false for consumers without an extracted server.
func (p *Prepared) CheckConfig(consumer string) (map[string]any, bool) {
	c := p.consumer(consumer)
	if c == nil || c.server == nil || c.server.metadata.Contract == nil {
		return nil, false
	}
	sources := maps.Clone(c.server.sources)
	if sources == nil {
		sources = map[string]string{}
	}
	entry, _ := filepath.Rel(c.Dir, filepath.Join(c.server.origin, c.server.entry))
	return map[string]any{"entry": filepath.ToSlash(entry), "sources": sources, "check": true}, true
}

func typeCheck(ctx context.Context, c *preparedConsumer, p *Prepared, copy string) error {
	env, err := commandenv.Environ(append(append([]string{}, p.options.Env...), "PYTHONDONTWRITEBYTECODE=1"))
	if err != nil {
		return err
	}
	languages := map[string]bool{}
	slots := map[string]map[string]string{}
	for target, files := range c.packages {
		switch target {
		case TargetTS, TargetUI:
			// Both Node slots are linked into one copy and checked by one TypeScript run.
			slots[slotPackages[target]] = files
			languages["typescript"] = true
		case TargetPy:
			languages["python"] = true
		}
	}
	if len(languages) == 0 {
		return nil
	}
	if _, err = snapshotSource(c.Name, c.Dir, copy); err != nil {
		return err
	}
	for _, language := range slices.Sorted(maps.Keys(languages)) {
		l := typeLanguages[language]
		program, err := l.localProgram(ctx)
		if err != nil {
			return err
		}
		switch language {
		case "typescript":
			if err = linkNodeModules(c.Dir, copy, slots, c.slot); err != nil {
				return fmt.Errorf("%s: %w", c.Name, err)
			}
		case "python":
			if err = writePythonSlot(copy, c.packages[TargetPy]); err != nil {
				return err
			}
			program = pythonEnvironment(c.Dir, program)
		}
		config := filepath.Join(p.root, "check-"+filepath.Base(copy)+"-"+language+".json")
		if err = writeJSON(config, map[string]any{"root": copy, "entry": "", "sources": map[string]string{}, "check": true}); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, program, filepath.Join(p.root, "tooling", language, l.runner), "check", config)
		command.Dir = copy
		command.Env = env
		command.Stdout = p.options.Stdout
		command.Stderr = p.options.Stderr
		if err = command.Run(); err != nil {
			return fmt.Errorf("%s does not type-check against its typed contracts: %w", c.Name, err)
		}
	}
	return nil
}

func pythonEnvironment(dir, fallback string) string {
	for current := dir; ; current = filepath.Dir(current) {
		for _, venv := range []string{".venv", "venv"} {
			if python := filepath.Join(current, venv, "bin", "python"); isFile(python) {
				return python
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if active := os.Getenv("VIRTUAL_ENV"); active != "" && isFile(filepath.Join(active, "bin", "python")) {
		return filepath.Join(active, "bin", "python")
	}
	return fallback
}

// linkNodeModules copies each slot package (by name) that source resolves into destination's node_modules
// with its generated files, then symlinks the rest of source's node_modules chain beside them.
// The copies come first so that the symlink pass never links an installed slot package.
func linkNodeModules(source, destination string, slots map[string]map[string]string, meta slotMetadata) error {
	for _, pkg := range slices.Sorted(maps.Keys(slots)) {
		installed, err := InstalledRuntime(source, pkg)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, "node_modules", pkg)
		if err = copyTree(installed, target); err != nil {
			return err
		}
		// The copied slot is exclusively ours; replace any prior consumer's files in this copy.
		if err = os.RemoveAll(filepath.Join(target, "generated")); err != nil {
			return err
		}
		if err = writeNodeSlot(target, pkg, slots[pkg], meta); err != nil {
			return err
		}
	}
	var link func(string, string) error
	link = func(from, to string) error {
		entries, err := os.ReadDir(from)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = os.MkdirAll(to, 0o755); err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".bin" {
				continue
			}
			src, dst := filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())
			if strings.HasPrefix(entry.Name(), "@") {
				if err = link(src, dst); err != nil {
					return err
				}
				continue
			}
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if err = os.Symlink(src, dst); err != nil {
				return err
			}
		}
		return nil
	}
	for dir := source; ; dir = filepath.Dir(dir) {
		if err := link(filepath.Join(dir, "node_modules"), filepath.Join(destination, "node_modules")); err != nil {
			return err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return nil
}
