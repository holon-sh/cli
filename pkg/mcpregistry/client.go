package mcpregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/holon-sh/cli/pkg/clientutil"
	"github.com/holon-sh/cli/pkg/protocol"
)

type HTTPError int

func (e HTTPError) Error() string { return fmt.Sprintf("MCP registry returned HTTP %d", e) }

type Record struct {
	Server json.RawMessage            `json:"server"`
	Meta   map[string]json.RawMessage `json:"_meta"`
}

func (r Record) Status() string {
	var official struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(r.Meta["io.modelcontextprotocol.registry/official"], &official)
	return official.Status
}

type Page struct {
	Servers  []Record `json:"servers"`
	Metadata struct {
		NextCursor string `json:"nextCursor"`
	} `json:"metadata"`
}

type Client struct {
	HTTP   *http.Client
	Origin string
}

func OfficialClient(transport http.RoundTripper) *Client {
	return &Client{Origin: protocol.MCPRegistryOrigin, HTTP: &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many MCP registry redirects")
		}
		if req.URL.Scheme != "https" || req.URL.Host != "registry.modelcontextprotocol.io" || req.URL.User != nil {
			return errors.New("MCP registry redirect must remain on the official HTTPS origin")
		}
		return nil
	}}}
}

func (c Client) read(ctx context.Context, path string, limit int64, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Origin, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return HTTPError(response.StatusCode)
	}
	data, err := clientutil.ReadLimited(response.Body, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}

func (c Client) Get(ctx context.Context, name, version string) (Record, error) {
	var record Record
	path := "/v0.1/servers/" + url.PathEscape(name) + "/versions/" + url.PathEscape(version)
	if err := c.read(ctx, path, MaxDescriptorBytes, &record); err != nil {
		return record, err
	}
	var identity struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(record.Server, &identity); err != nil {
		return record, err
	}
	if identity.Name != name || identity.Version != version {
		return record, errors.New("MCP registry returned a different server name or version")
	}
	return record, nil
}

func (c Client) List(ctx context.Context, search, cursor string, limit int) (Page, error) {
	var page Page
	query := url.Values{"version": {"latest"}, "limit": {strconv.Itoa(limit)}}
	if search != "" {
		query.Set("search", search)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if err := c.read(ctx, "/v0.1/servers?"+query.Encode(), 4<<20, &page); err != nil {
		return page, err
	}
	if page.Servers == nil || len(page.Servers) > limit {
		return page, errors.New("invalid MCP registry page")
	}
	return page, nil
}
