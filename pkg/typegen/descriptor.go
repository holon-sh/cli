package typegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpcontract"
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

// validRegistryRef also requires a name and an exact version that the MCP Registry accepts.
func validRegistryRef(r protocol.RegistryRef) error {
	if len(r.Name) > 200 || !serverNamePattern.MatchString(r.Name) {
		return errors.New("server name must have the form namespace/name")
	}
	if len(r.Version) > 255 || strings.ContainsAny(r.Version, "^~<>=*| \t\r\n") || wildcardVersion.MatchString(r.Version) {
		return errors.New("server version must be exact; ranges and latest are unsupported")
	}
	return r.Validate()
}

// Server is how a server describes itself, in its server.json or the serverInfo it answers.
type Server struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	WebsiteURL  string `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
}

type descriptor struct {
	Server
	Meta map[string]json.RawMessage `json:"_meta"`
}

func (d *descriptor) Identity() mcpcontract.Identity {
	return mcpcontract.Identity{Name: d.Name, Version: d.Version}
}
func ParseDescriptor(data []byte) (*descriptor, error) {
	var d descriptor
	if len(data) > mcpregistry.MaxDescriptorBytes {
		return nil, errors.New("server descriptor exceeds 1 MiB")
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if err := validRegistryRef(protocol.RegistryRef{Name: d.Name, Version: d.Version}); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(d.Description); n < 1 || n > 100 {
		return nil, errors.New("server description must contain 1 to 100 characters")
	}
	return &d, nil
}

// readDescriptor parses dir's server.json, or returns nil when it has none.
func readDescriptor(dir string) (*descriptor, error) {
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
