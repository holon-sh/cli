package typegen

import (
	"context"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"maps"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Check verifies generated contracts exclusively in temporary copies.
func Check(ctx context.Context, m Manifest, o Options) error {
	o = o.defaults()
	prepared, err := Prepare(ctx, m, o)
	if err != nil {
		return err
	}
	defer prepared.Close()
	for _, app := range prepared.Manifest.Apps {
		if p := app.prepared; p != nil {
			if p.diagnostic != "" {
				fmt.Fprintf(o.Stdout, "%s: %s\n", app.Name, p.diagnostic)
			}
			if err = typeCheck(ctx, app, prepared, o); err != nil {
				return err
			}
		}
	}
	if !o.NoPublished {
		if err = checkPublishedTypes(ctx, prepared.Manifest, o); err != nil {
			return err
		}
	}
	fmt.Fprintln(o.Stdout, "Typed contracts check passed.")
	return nil
}
func typeCheck(ctx context.Context, app App, prepared *Prepared, o Options) error {
	p := app.prepared
	env, err := commandenv.Environ(append(append([]string{}, o.Env...), "PYTHONDONTWRITEBYTECODE=1"))
	if err != nil {
		return err
	}
	for _, language := range slices.Sorted(maps.Keys(p.packages)) {
		l := typeLanguages[language]
		program, err := l.localProgram(ctx)
		if err != nil {
			return err
		}
		entry := p.entry
		switch language {
		case "typescript":
			if strings.HasSuffix(entry, ".py") {
				entry = ""
			}
			if err = linkNodeModules(app.Path, p.root, p.packages[language], p.slot); err != nil {
				return fmt.Errorf("%s: %w", app.Name, err)
			}
		case "python":
			if err = writePythonSlot(p.root, p.packages[language]); err != nil {
				return err
			}
			program = pythonEnvironment(app.Path, program)
		}
		sources := map[string]string{}
		for name, digest := range p.sources {
			if language == "python" {
				name = filepath.Join(p.root, name)
			}
			sources[name] = digest
		}
		config := filepath.Join(prepared.root, "check-"+app.Name+"-"+language+".json")
		if err = writeJSON(config, map[string]any{"root": p.root, "entry": entry, "sources": sources, "check": true}); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, program, filepath.Join(prepared.root, "tooling", language, l.runner), "check", config)
		command.Dir = p.root
		command.Env = env
		command.Stdout = o.Stdout
		command.Stderr = o.Stderr
		if err = command.Run(); err != nil {
			return fmt.Errorf("%s does not type-check against its typed contracts: %w", app.Name, err)
		}
	}
	return nil
}
func pythonEnvironment(app, fallback string) string {
	for dir := app; ; dir = filepath.Dir(dir) {
		for _, venv := range []string{".venv", "venv"} {
			if python := filepath.Join(dir, venv, "bin", "python"); isFile(python) {
				return python
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if active := os.Getenv("VIRTUAL_ENV"); active != "" && isFile(filepath.Join(active, "bin", "python")) {
		return filepath.Join(active, "bin", "python")
	}
	return fallback
}
func linkNodeModules(source, destination string, files map[string]string, meta slotMetadata) error {
	pkg, err := nodeSlot(source)
	if err != nil {
		return err
	}
	target := filepath.Join(destination, "node_modules", nodePackage)
	if err = copyTree(pkg, target); err != nil {
		return err
	}
	// The copied slot is exclusively ours; replace any prior app's files in this snapshot.
	if err = os.RemoveAll(filepath.Join(target, "generated")); err != nil {
		return err
	}
	if err = writeNodeSlot(target, files, meta); err != nil {
		return err
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
		if err = link(filepath.Join(dir, "node_modules"), filepath.Join(destination, "node_modules")); err != nil {
			return err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return nil
}
func hasContract(m *mcpcontract.Metadata) bool { return m != nil && m.Contract != nil }
func publishedTypeMetadata(ctx context.Context, app App, o Options) (*mcpcontract.Metadata, error) {
	d := app.descriptor
	if d == nil {
		return nil, nil
	}
	var metadata *mcpcontract.Metadata
	var err error
	if d.Types != "" {
		metadata, err = fetchContract(ctx, d.Types, o)
	} else {
		for _, pkg := range d.Packages {
			if pkg.RegistryType == "oci" {
				metadata, err = resolveTypeMetadata(ctx, pkg.Identifier, o)
				break
			}
		}
	}
	var httpError HTTPError
	var registryError *errcode.ErrorResponse
	if errors.Is(err, errdef.ErrNotFound) || errors.As(err, &httpError) && (httpError == 401 || httpError == 403 || httpError == 404) || errors.As(err, &registryError) && (registryError.StatusCode == 401 || registryError.StatusCode == 403 || registryError.StatusCode == 404) {
		return nil, nil
	}
	if err == nil && metadata != nil && metadata.Identity != d.Identity() {
		return nil, fmt.Errorf("%s descriptor identity does not match the retained image contract; use the original release identity or a new image tag", app.Name)
	}
	return metadata, err
}
func checkPublishedTypes(ctx context.Context, m Manifest, o Options) error {
	published := map[string]*mcpcontract.Metadata{}
	for _, app := range m.Apps {
		if app.prepared == nil {
			continue
		}
		metadata, err := publishedTypeMetadata(ctx, app, o)
		if err != nil {
			return fmt.Errorf("%s published contract: %w", app.Name, err)
		}
		published[app.Name] = metadata
	}
	var problems []string
	for _, app := range m.Apps {
		p := app.prepared
		if p == nil || !hasContract(p.metadata) {
			continue
		}
		canonical, err := mcptype.Canonical(ctx, map[string]any{"contract": p.metadata.Contract})
		if err != nil {
			return err
		}
		fmt.Fprintf(o.Stdout, "%s contract %s\n", app.Name, canonical.Digest)
		if old := published[app.Name]; hasContract(old) {
			changes, err := mcptype.Changes(ctx, mcpcontract.Expectation{Identity: p.metadata.Identity, Contract: *old.Contract}, p.metadata)
			if err != nil {
				return err
			}
			if len(changes) > 0 {
				problems = append(problems, fmt.Sprintf("%s has changed its published typed contract (%s)", app.Name, changeText(changes)))
			}
		}
		for name, caller := range published {
			if name == app.Name || caller == nil {
				continue
			}
			if expected, ok := caller.Requires[app.Name]; ok {
				changes, err := mcptype.Changes(ctx, expected, p.metadata)
				if err != nil {
					return err
				}
				if len(changes) > 0 {
					problems = append(problems, fmt.Sprintf("%s has changed the typed contract expected by %s (%s); rebuild and publish the caller", app.Name, name, changeText(changes)))
				}
			}
		}
	}
	for _, problem := range problems {
		fmt.Fprintln(o.Stdout, problem)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d typed contract change(s) against published images; use --no-published to skip this comparison", len(problems))
	}
	return nil
}
func changeText(changes []mcptype.Change) string {
	texts := make([]string, len(changes))
	for i, change := range changes {
		texts[i] = change.Text
	}
	return strings.Join(texts, ", ")
}
