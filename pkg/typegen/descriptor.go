package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const RegistryOrigin = "https://registry.modelcontextprotocol.io"
const PublisherMetadata = "io.modelcontextprotocol.registry/publisher-provided"
const MaxDescriptorBytes = 1 << 20

// ownContract is the reserved key under which generated clients expose the app's own server.
const ownContract = "self"

var serverNamePattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
var wildcardVersion = regexp.MustCompile(`(^|\.)[xX](\.|$)`)

type Reference struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Registry string `json:"registry,omitempty"`
}

func (r Reference) Identity() mcpcontract.Identity {
	return mcpcontract.Identity{Name: r.Name, Version: r.Version}
}
func (r Reference) RegistryURL() string {
	if r.Registry == "" {
		return RegistryOrigin
	}
	return strings.TrimRight(r.Registry, "/")
}
func (r Reference) Validate() error {
	if len(r.Name) > 200 || !serverNamePattern.MatchString(r.Name) {
		return errors.New("server name must have the form namespace/name")
	}
	if r.Version == "" || len(r.Version) > 255 || strings.EqualFold(r.Version, "latest") || strings.ContainsAny(r.Version, "^~<>=*| \t\r\n") || wildcardVersion.MatchString(r.Version) {
		return errors.New("server version must be exact; ranges and latest are unsupported")
	}
	u, err := url.Parse(r.RegistryURL())
	if err == nil {
		host := u.Hostname()
		loopback := host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host).IsLoopback()
		if u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http" && loopback) {
			return nil
		}
	}
	return errors.New("MCP registry must be an HTTPS URL (loopback HTTP is allowed)")
}

type Descriptor struct {
	Name        string                     `json:"name"`
	Version     string                     `json:"version"`
	Description string                     `json:"description"`
	Packages    []Package                  `json:"packages"`
	Remotes     []Transport                `json:"remotes"`
	Meta        map[string]json.RawMessage `json:"_meta"`
	Requires    map[string]Reference       `json:"-"`
	Types       string                     `json:"-"`
	Raw         json.RawMessage            `json:"-"`
}
type Package struct {
	RegistryType string    `json:"registryType"`
	Identifier   string    `json:"identifier"`
	Transport    Transport `json:"transport"`
}
type Transport struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}
type HolonMetadata struct {
	Requires map[string]Reference `json:"requires"`
	Types    string               `json:"types,omitempty"`
	Egress   json.RawMessage      `json:"egress,omitempty"`
}

func (d *Descriptor) Identity() mcpcontract.Identity {
	return mcpcontract.Identity{Name: d.Name, Version: d.Version}
}
func validateTypesURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("sh.holon.types must be an HTTPS URL without credentials or a fragment")
	}
	return nil
}
func ParseDescriptor(data []byte) (*Descriptor, error) {
	var d Descriptor
	if len(data) > MaxDescriptorBytes {
		return nil, errors.New("server descriptor exceeds 1 MiB")
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if err := (Reference{Name: d.Name, Version: d.Version}).Validate(); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(d.Description); n < 1 || n > 100 {
		return nil, errors.New("server description must contain 1 to 100 characters")
	}
	if raw := d.Meta[PublisherMetadata]; raw != nil {
		if len(raw) > 4096 {
			return nil, errors.New("publisher-provided metadata exceeds 4096 bytes")
		}
		var publisher map[string]json.RawMessage
		if err := json.Unmarshal(raw, &publisher); err != nil {
			return nil, err
		}
		if extension := publisher["sh.holon"]; extension != nil {
			var h HolonMetadata
			if err := decodeStrict(extension, &h); err != nil {
				return nil, fmt.Errorf("sh.holon metadata: %w", err)
			}
			d.Requires = h.Requires
			d.Types = h.Types
		}
	}
	if d.Types != "" {
		if err := validateTypesURL(d.Types); err != nil {
			return nil, err
		}
	}
	for name, ref := range d.Requires {
		if len(name) > 49 || !appName.MatchString(name) {
			return nil, fmt.Errorf("invalid dependency name %q", name)
		}
		if name == ownContract {
			return nil, fmt.Errorf("sh.holon.requires: %q is reserved for the app's own server; rename the dependency", ownContract)
		}
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf("dependency %s: %w", name, err)
		}
	}
	d.Raw = data
	return &d, nil
}

var errIdentityMismatch = errors.New("MCP registry returned a different server name or version")

type HTTPError int

func (e HTTPError) Error() string { return fmt.Sprintf("HTTP %d", e) }

type Client struct {
	HTTP   *http.Client
	Origin string
}

func (c Client) Get(ctx context.Context, name, version string) (*Descriptor, error) {
	origin := c.Origin
	if origin == "" {
		origin = RegistryOrigin
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+"/v0.1/servers/"+url.PathEscape(name)+"/versions/"+url.PathEscape(version), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, HTTPError(response.StatusCode)
	}
	data, err := readLimited(response.Body, MaxDescriptorBytes)
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
		return nil, errIdentityMismatch
	}
	return d, nil
}
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("document exceeds %d bytes", limit)
	}
	return data, err
}
