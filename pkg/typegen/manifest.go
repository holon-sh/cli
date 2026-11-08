package typegen

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

var (
	goModulePath = regexp.MustCompile(`(?m)^\s*module\s+"?([^\s"]+)"?`)
	tomlTable    = regexp.MustCompile(`^\s*\[`)
	tomlProject  = regexp.MustCompile(`^\s*\[project\]\s*(#.*)?$`)
	tomlString   = regexp.MustCompile(`^\s*(name|version)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

// manifestIdentity is the name and version a project's manifest gives it, read from the manifest
// of the first target that names one: package.json for Node slots, pyproject.toml's [project]
// table for Python and go.mod's module path for Go. Projects no manifest names fall back to the
// consumer's name, and versions no manifest gives are unversioned.
func manifestIdentity(dir string, targets []string, fallback string) (mcpcontract.Identity, error) {
	for _, target := range targets {
		var identity mcpcontract.Identity
		var err error
		switch target {
		case TargetTS, TargetUI:
			var manifest packageJSON
			err = readTypeJSON(filepath.Join(dir, "package.json"), &manifest)
			identity = mcpcontract.Identity{Name: manifest.Name, Version: manifest.Version}
		case TargetPy:
			var data []byte
			data, err = os.ReadFile(filepath.Join(dir, "pyproject.toml"))
			identity = pyprojectIdentity(data)
		case TargetGo:
			var data []byte
			data, err = os.ReadFile(filepath.Join(dir, "go.mod"))
			if match := goModulePath.FindSubmatch(data); match != nil {
				identity.Name = string(match[1])
			}
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return mcpcontract.Identity{}, err
		}
		if identity.Name != "" {
			identity.Version = cmp.Or(identity.Version, unversioned)
			return identity, nil
		}
	}
	return mcpcontract.Identity{Name: fallback, Version: unversioned}, nil
}

// pyprojectIdentity reads the [project] table's name and version, each a quoted string on its own
// line as a PEP 621 project declares them. A dynamic version is left empty.
func pyprojectIdentity(data []byte) mcpcontract.Identity {
	var identity mcpcontract.Identity
	inProject := false
	for scanner := bufio.NewScanner(bytes.NewReader(data)); scanner.Scan(); {
		line := scanner.Text()
		if tomlTable.MatchString(line) {
			inProject = tomlProject.MatchString(line)
			continue
		}
		if !inProject {
			continue
		}
		if match := tomlString.FindStringSubmatch(line); match != nil {
			value := match[2] + match[3]
			if match[1] == "name" {
				identity.Name = value
			} else {
				identity.Version = value
			}
		}
	}
	return identity
}
