package typegen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
			slot, err := InstalledRuntime(dir, nodePackages[target])
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
	if err := validateEnv(o.Env); err != nil {
		return err
	}
	slices.Sort(languages)
	for _, language := range slices.Compact(languages) {
		l := typeLanguages[language]
		var program string
		if language == "python" {
			program = pythonEnvironment(dir, "")
		}
		if program == "" {
			if program, err = l.localProgram(ctx); err != nil {
				return err
			}
		}
		checker, err := tooling.File(language, l.checker)
		if err != nil {
			return err
		}
		path := filepath.Join(root, l.checker)
		if err = os.WriteFile(path, checker, 0o600); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, program, path, "check", "")
		command.Dir = dir
		command.Env = append(append(command.Environ(), o.Env...), "PYTHONDONTWRITEBYTECODE=1")
		command.Stdout = o.Stdout
		command.Stderr = o.Stderr
		if err = command.Run(); err != nil {
			return fmt.Errorf("%s does not type-check against its typed contracts: %w", filepath.Base(dir), err)
		}
	}
	fmt.Fprintf(o.Stdout, "%s: typed contracts check passed.\n", filepath.Base(dir))
	return nil
}

// CheckEntry is the consumer's own server entry, relative to the consumer directory, and its language, for a
// type check against the dependencies installed in its image. It reports false for consumers without an
// extracted server.
func (p *Prepared) CheckEntry(consumer string) (entry, language string, ok bool) {
	c := p.consumer(consumer)
	if c == nil || c.server == nil || c.server.metadata.Schema == nil {
		return "", "", false
	}
	entry, _ = filepath.Rel(c.Dir, filepath.Join(c.server.origin, c.server.entry))
	return filepath.ToSlash(entry), c.server.language, true
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
