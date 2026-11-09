package typegen

import (
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var serverNamePattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
var wildcardVersion = regexp.MustCompile(`(^|\.)[xX](\.|$)`)

func ValidServerName(name string) bool {
	return len(name) <= 200 && serverNamePattern.MatchString(name)
}

// validRegistryRef also requires a name and an exact version that the MCP Registry accepts.
func validRegistryRef(r protocol.RegistryRef) error {
	if !ValidServerName(r.Name) {
		return errors.New("server name must have the form namespace/name")
	}
	if len(r.Version) > 255 || strings.ContainsAny(r.Version, "^~<>=*| \t\r\n") || wildcardVersion.MatchString(r.Version) {
		return errors.New("server version must be exact; ranges and latest are unsupported")
	}
	return r.Validate()
}

// ParseDescriptor reads a server.json or serverInfo, which must also have a name, an exact version
// and a description that the MCP Registry accepts.
func ParseDescriptor(data []byte) (*mcpregistry.Descriptor, error) {
	if len(data) > mcpregistry.MaxDescriptorBytes {
		return nil, errors.New("server descriptor exceeds 1 MiB")
	}
	d, err := mcpregistry.ParseLocalDescriptor(data)
	if err != nil {
		return nil, err
	}
	if err := validRegistryRef(protocol.RegistryRef{Name: d.Name, Version: d.Version}); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(d.Description); n < 1 || n > 100 {
		return nil, errors.New("server description must contain 1 to 100 characters")
	}
	return d, nil
}

// readDescriptor parses dir's server.json, or returns nil when it has none.
func readDescriptor(dir string) (*mcpregistry.Descriptor, error) {
	path := filepath.Join(dir, "server.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := ParseDescriptor(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}
