package typegen

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/holon-sh/cli/pkg/tooling"
)

func Check(ctx context.Context, dir string, options Options) error {
	o := options.defaults()
	targets, err := DeclaredTargets(dir)
	if err != nil {
		return err
	}
	var languages []string
	for _, target := range targets {
		switch target {
		case TargetGo:
			return fmt.Errorf("%s: Go clients are generated; run go test ./... to check the project", dir)
		case TargetTS, TargetUI:
			slot, err := installedRuntime(dir, nodePackages[target])
			if err != nil {
				return err
			}
			var meta slotMetadata
			if err := readTypeJSON(filepath.Join(slot, "generated", "holon-generated.json"), &meta); err != nil || meta.Placeholder {
				return fmt.Errorf("%s has no generated clients; run holon types generate", slot)
			}
			languages = append(languages, "typescript")
		case TargetPy:
			if !generatedPython(filepath.Join(dir, "holon")) {
				return fmt.Errorf("%s has no generated clients; run holon types generate", dir)
			}
			languages = append(languages, "python")
		}
	}
	root, err := os.MkdirTemp("", "holon-types-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
		return err
	}
	if err := validateEnv(o.Env); err != nil {
		return err
	}
	slices.Sort(languages)
	for _, language := range slices.Compact(languages) {
		l := typeLanguages[language]
		program, err := l.localProgram(ctx)
		if err != nil {
			return err
		}
		if language == "python" {
			program = pythonEnvironment(dir, program)
		}
		config := filepath.Join(root, language+"-check.json")
		if err = writeJSON(config, map[string]any{"root": dir, "entry": "", "sources": map[string]string{}, "check": true}); err != nil {
			return err
		}
		command := l.command(ctx, program, filepath.Join(root, "tooling"), language, l.checker, config)
		command.Dir = dir
		command.Env = append(append(command.Environ(), o.Env...), "PYTHONDONTWRITEBYTECODE=1")
		command.Stdout = o.Stdout
		command.Stderr = o.Stderr
		if err = command.Run(); err != nil {
			return fmt.Errorf("%s does not type-check against its typed contracts: %w", filepath.Base(dir), err)
		}
	}
	fmt.Fprintln(o.Stdout, "Typed contracts check passed.")
	return nil
}

// CheckConfig is the runner's check configuration ({entry, sources, check}) for the consumer's own
// server, for a type check inside an image build against the dependencies installed there. Paths are
// relative to the consumer directory. It reports false for consumers without an extracted server.
func (p *Prepared) CheckConfig(consumer string) (map[string]any, bool) {
	c := p.consumer(consumer)
	if c == nil || c.server == nil || c.server.metadata.Schema == nil {
		return nil, false
	}
	sources := map[string]string{}
	maps.Copy(sources, c.server.sources)
	entry, _ := filepath.Rel(c.Dir, filepath.Join(c.server.origin, c.server.entry))
	return map[string]any{"entry": filepath.ToSlash(entry), "sources": sources, "check": true}, true
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
