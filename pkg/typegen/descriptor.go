package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxDescriptorBytes = 1 << 20

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
	Packages []registryPackage          `json:"packages"`
	Remotes  []transport                `json:"remotes"`
	Meta     map[string]json.RawMessage `json:"_meta"`
}
type registryPackage struct {
	RegistryType string `json:"registryType"`
	Identifier   string `json:"identifier"`
}
type transport struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

func (d *descriptor) Identity() mcpcontract.Identity {
	return mcpcontract.Identity{Name: d.Name, Version: d.Version}
}
func ParseDescriptor(data []byte) (*descriptor, error) {
	var d descriptor
	if len(data) > maxDescriptorBytes {
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

type Client struct {
	HTTP   *http.Client
	Origin string
}

func (c Client) Get(ctx context.Context, name, version string) (*descriptor, error) {
	origin := protocol.RegistryRef{Registry: c.Origin}.RegistryURL()
	data, err := fetchJSON(ctx, c.HTTP, origin+"/v0.1/servers/"+url.PathEscape(name)+"/versions/"+url.PathEscape(version))
	if err != nil {
		return nil, err
	}
	var record struct {
		Server json.RawMessage `json:"server"`
	}
	if err = json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	d, err := ParseDescriptor(record.Server)
	if err != nil {
		return nil, err
	}
	if d.Name != name || d.Version != version {
		return nil, errors.New("MCP registry returned a different server name or version")
	}
	return d, nil
}

// fetchJSON bounds registry records.
func fetchJSON(ctx context.Context, client *http.Client, location string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := cmp.Or(client, http.DefaultClient).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return clientutil.ReadLimited(response.Body, maxDescriptorBytes)
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
