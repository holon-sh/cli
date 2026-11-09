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

// item is a line that opens an apps list item, capturing its indentation.
var item = regexp.MustCompile(`^(\s*)- `)

// Register adds app, kept at path relative to the manifest's directory, to the holon.yaml at
// manifest, creating the file when there is none. It appends to an existing file's apps list in
// place, keeping the rest of the file as written, and fails when that list does not end the file.
func Register(manifest, app, path string) error {
	entry := fmt.Sprintf("- name: %s\n  path: %s\n  access:\n    server: {}\n", app, path)
	data, err := os.ReadFile(manifest)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(manifest, []byte("apps:\n"+indent(entry, "  ")), 0o644)
	}
	if err != nil {
		return err
	}
	before, err := appNames(data)
	if err != nil {
		return fmt.Errorf("%s: %w", manifest, err)
	}
	if slices.Contains(before, app) {
		return fmt.Errorf("%s already has an app named %s", manifest, app)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := slices.IndexFunc(lines, func(line string) bool { return strings.TrimRight(line, " ") == "apps:" })
	var prefix *string
	for i := start + 1; start >= 0 && i < len(lines); i++ {
		if match := item.FindStringSubmatch(lines[i]); match != nil && prefix == nil {
			prefix = &match[1]
		}
		// Another top-level key follows the list, so appending would put the app under it.
		if line := lines[i]; line != "" && line[0] != ' ' && line[0] != '#' && line[0] != '-' {
			start = -1
		}
	}
	if prefix == nil {
		prefix = new("  ")
	}
	updated := []byte(strings.Join(lines, "\n") + "\n" + indent(entry, *prefix))
	after, err := appNames(updated)
	if start < 0 || err != nil || len(after) != len(before)+1 || !slices.Contains(after, app) {
		return fmt.Errorf("could not add %s to the apps list in %s; add it there:\n%s", app, manifest, indent(entry, "  "))
	}
	return os.WriteFile(manifest, updated, 0o644)
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
