package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

type manifestApps struct {
	Apps []struct {
		Name string `json:"name"`
	} `json:"apps"`
}

var item = regexp.MustCompile(`^(\s*)- `)

// Manifest is a holon.yaml with an app added, to write once the app is.
type Manifest struct {
	Path string
	data []byte
}

// Write writes the manifest.
func (m Manifest) Write() error {
	return os.WriteFile(m.Path, m.data, 0o644)
}

// AddApp adds app, kept at path relative to the manifest's directory, to the holon.yaml at
// manifest, which it creates when there is none. It appends to an existing file's apps list in
// place, keeping the rest of the file as written, and fails when that list does not end the file.
func AddApp(manifest, app, path string) (Manifest, error) {
	entry := fmt.Sprintf("- name: %s\n  path: %q\n  access:\n    server: {}\n", app, path)
	data, err := os.ReadFile(manifest)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{manifest, []byte("apps:\n" + indent(entry, "  "))}, nil
	}
	if err != nil {
		return Manifest{}, err
	}
	before, err := appNames(data)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", manifest, err)
	}
	if slices.Contains(before, app) {
		return Manifest{}, fmt.Errorf("%s already has an app named %s", manifest, app)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := slices.IndexFunc(lines, func(line string) bool { return strings.TrimRight(line, " ") == "apps:" })
	prefix := "  "
	for _, line := range lines[start+1:] {
		if match := item.FindStringSubmatch(line); match != nil {
			prefix = match[1]
			break
		}
	}
	updated := []byte(strings.Join(lines, "\n") + "\n" + indent(entry, prefix))
	after, err := appNames(updated)
	if start < 0 || err != nil || len(after) != len(before)+1 || !slices.Contains(after, app) {
		return Manifest{}, fmt.Errorf("could not add %s to the apps list in %s; add it there:\n%s", app, manifest, indent(entry, "  "))
	}
	return Manifest{manifest, updated}, nil
}

func appNames(data []byte) ([]string, error) {
	var project manifestApps
	if err := yaml.Unmarshal(data, &project); err != nil {
		return nil, err
	}
	var names []string
	for _, app := range project.Apps {
		names = append(names, app.Name)
	}
	return names, nil
}

func indent(text, prefix string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "")
}
