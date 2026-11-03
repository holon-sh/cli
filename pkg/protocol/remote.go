package protocol

import (
	"cmp"
	"errors"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

func ValidateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.RawPath != "" {
		return errors.New("remote endpoint must be an HTTPS URL without credentials, query, or fragment")
	}
	port, _ := strconv.ParseInt(cmp.Or(u.Port(), "443"), 10, 32)
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return errors.New("remote endpoints require DNS hostnames")
	}
	_, err = (Egress{{Host: u.Hostname(), Ports: []int32{int32(port)}}}).Normalize()
	return err
}

func validRemoteHeader(name string) bool {
	name = strings.ToLower(name)
	if !httpguts.ValidHeaderFieldName(name) || slices.ContainsFunc(
		[]string{"x-holon-", "x-forwarded-", "x-envoy-", "x-b3-", "mcp-", "sec-"},
		func(prefix string) bool { return strings.HasPrefix(name, prefix) },
	) {
		return false
	}
	return !slices.Contains([]string{
		"host", "connection", "proxy-connection", "proxy-authorization",
		"proxy-authenticate", "cookie", "set-cookie", "forwarded",
		"origin", "referer", "content-length", "content-type",
		"accept", "accept-encoding", "transfer-encoding", "te",
		"trailer", "upgrade", "keep-alive", "baggage",
		"traceparent", "tracestate",
	}, name)
}

func (r Remote) Validate(env map[string]Input) error {
	declared := func(name string) (string, bool) {
		_, ok := env[name]
		return "holon.invalid", ok
	}
	endpoint, complete := Expand(r.URL, declared)
	if !complete {
		return errors.New("remote endpoint names an input without a value")
	}
	if err := ValidateRemoteURL(endpoint); err != nil {
		return err
	}
	for name, template := range r.Headers {
		if _, complete := Expand(template, declared); !validRemoteHeader(name) || !complete {
			return errors.New("invalid remote header binding")
		}
		if name == "Authorization" && !slices.ContainsFunc(TemplateInputs(template), func(name string) bool { return env[name].IsSecret }) {
			return errors.New("authorization header requires a secret reference")
		}
	}
	return nil
}
