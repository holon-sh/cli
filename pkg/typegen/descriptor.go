package typegen

import (
	"bytes"
	"cmp"
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
	return strings.TrimRight(cmp.Or(r.Registry, RegistryOrigin), "/")
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

type descriptor struct {
	Name        string                     `json:"name"`
	Version     string                     `json:"version"`
	Description string                     `json:"description"`
	Packages    []registryPackage          `json:"packages"`
	Remotes     []transport                `json:"remotes"`
	Meta        map[string]json.RawMessage `json:"_meta"`
	Requires    map[string]Reference       `json:"-"`
}
type registryPackage struct {
	RegistryType string `json:"registryType"`
	Identifier   string `json:"identifier"`
}
type transport struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}
type holonMetadata struct {
	Requires map[string]Reference `json:"requires"`
	Egress   json.RawMessage      `json:"egress,omitempty"`
}

func (d *descriptor) Identity() mcpcontract.Identity {
	return mcpcontract.Identity{Name: d.Name, Version: d.Version}
}
func ParseDescriptor(data []byte) (*descriptor, error) {
	var d descriptor
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
			var h holonMetadata
			// Unmarshal above already guarantees a single JSON value.
			decoder := json.NewDecoder(bytes.NewReader(extension))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&h); err != nil {
				return nil, fmt.Errorf("sh.holon metadata: %w", err)
			}
			d.Requires = h.Requires
		}
	}
	for name, ref := range d.Requires {
		if len(name) > 49 || !appName.MatchString(name) {
			return nil, fmt.Errorf("invalid dependency name %q", name)
		}
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf("dependency %s: %w", name, err)
		}
	}
	return &d, nil
}

type HTTPError int

func (e HTTPError) Error() string { return fmt.Sprintf("HTTP %d", e) }

type Client struct {
	HTTP   *http.Client
	Origin string
}

func (c Client) Get(ctx context.Context, name, version string) (*descriptor, error) {
	origin := (Reference{Registry: c.Origin}).RegistryURL()
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
		return nil, HTTPError(response.StatusCode)
	}
	return readLimited(response.Body, MaxDescriptorBytes)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("document exceeds %d bytes", limit)
	}
	return data, err
}
