// Package typegen prepares portable typed MCP clients without a Holon installation.
package typegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"sigs.k8s.io/yaml"
)

const manifestFileName = "holon.yaml"
const dockerignoreFile = ".dockerignore"

var appName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

type Manifest struct {
	Apps []App `json:"apps"`
	file string
	// pathRequiresOnly expands and checks requires only for path apps, the listed directories
	// (standalone mode); registry dependencies' own requires are not followed.
	pathRequiresOnly bool
}
type App struct {
	Name        string          `json:"name"`
	Path        string          `json:"path,omitempty"`
	Image       string          `json:"image,omitempty"`
	Registry    *RegistrySource `json:"registry,omitempty"`
	Remote      *Remote         `json:"remote,omitempty"`
	Mount       string          `json:"mount,omitempty"`
	Port        *int            `json:"port,omitempty"`
	Access      json.RawMessage `json:"access,omitempty"`
	OAuth       json.RawMessage `json:"oauth,omitempty"`
	Headers     json.RawMessage `json:"headers,omitempty"`
	Egress      json.RawMessage `json:"egress,omitempty"`
	Run         json.RawMessage `json:"run,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	unavailable string
	// inferred holds requires the standalone CLI derived from the app's own source: every
	// apps.<name> reference that names another listed directory (Directories). Merged into
	// the descriptor's requires when the graph is resolved; never set for manifest apps.
	inferred   map[string]Reference
	descriptor *Descriptor
	prepared   *preparedTypes
}
type RegistrySource struct {
	Reference
	Package json.RawMessage `json:"package,omitempty"`
	Remote  json.RawMessage `json:"remote,omitempty"`
}
type Remote struct {
	URL string `json:"url"`
}
type manifestApp = App

func decodeStrict(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected a single JSON document")
	}
	return nil
}

// ParseManifest validates the source declarations while preserving host-specific fields.
func ParseManifest(data []byte, source string) (Manifest, error) {
	var m Manifest
	data, err := yaml.YAMLToJSONStrict(data)
	if err == nil {
		err = decodeStrict(data, &m)
	}
	if err != nil {
		return m, fmt.Errorf("%s: %w", source, err)
	}
	if len(m.Apps) == 0 {
		return m, errors.New("apps must contain at least one entry")
	}
	names := map[string]bool{}
	for _, a := range m.Apps {
		if len(a.Name) > 49 || !appName.MatchString(a.Name) {
			return m, fmt.Errorf("app name %q must be a DNS label of at most 49 characters", a.Name)
		}
		if names[a.Name] {
			return m, fmt.Errorf("duplicate app name %q", a.Name)
		}
		names[a.Name] = true
		sources := 0
		for _, present := range []bool{a.Path != "", a.Image != "", a.Registry != nil, a.Remote != nil} {
			if present {
				sources++
			}
		}
		if sources != 1 {
			return m, fmt.Errorf("%s: declare exactly one of path, image, registry, or remote", a.Name)
		}
		if filepath.IsAbs(a.Path) {
			return m, fmt.Errorf("%s.path must be a relative directory", a.Name)
		}
		if a.Registry != nil {
			if err := a.Registry.Validate(); err != nil {
				return m, fmt.Errorf("%s.registry: %w", a.Name, err)
			}
		}
	}
	return m, nil
}

// LoadManifest resolves app paths relative to the manifest, never the process working directory.
func LoadManifest(arg string) (Manifest, error) {
	if arg == "" {
		arg = manifestFileName
		if _, err := os.Lstat(arg); errors.Is(err, os.ErrNotExist) {
			arg = "holon.yml"
		}
	}
	absolute, err := filepath.Abs(arg)
	if err != nil {
		return Manifest{}, err
	}
	if err = regularNoSymlink(absolute, "manifest"); err != nil {
		return Manifest{}, err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return Manifest{}, err
	}
	m, err := ParseManifest(data, absolute)
	if err != nil {
		return m, err
	}
	m.file = absolute
	for i := range m.Apps {
		if m.Apps[i].Path != "" {
			m.Apps[i].Path = filepath.Join(filepath.Dir(absolute), m.Apps[i].Path)
		}
	}
	return m, nil
}

func regularNoSymlink(path, kind string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file: %s", kind, path)
	}
	return nil
}
func readTypeJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func pathInsideContext(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}
