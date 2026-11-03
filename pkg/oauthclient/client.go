package oauthclient

import (
	"bytes"
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const DefaultHTTPTimeout = 10 * time.Second

var ErrUnavailable = errors.New("token introspection unavailable")

type boundedTransport struct {
	transport http.RoundTripper
}

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !HTTPSURL(r.URL.String()) {
		return nil, errors.New("HTTPS is required")
	}
	response, err := t.transport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	_ = response.Body.Close()
	if err != nil || len(body) > 64<<10 {
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func HTTPClient(caBundle string, original *http.Client) (*http.Client, error) {
	client := *cmp.Or(original, http.DefaultClient)
	if client.Timeout <= 0 {
		client.Timeout = DefaultHTTPTimeout
	}
	client.Timeout = min(client.Timeout, DefaultHTTPTimeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	bounded := boundedTransport{transport: cmp.Or(client.Transport, http.DefaultTransport)}
	if caBundle != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM([]byte(caBundle)) {
			return nil, errors.New("invalid OAuth CA bundle")
		}
		base, ok := bounded.transport.(*http.Transport)
		if !ok {
			return nil, errors.New("OAuth CA bundle requires an HTTP transport")
		}
		cloned := base.Clone()
		cloned.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		bounded.transport = cloned
	}
	client.Transport = bounded
	return &client, nil
}

func HTTPSURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func ValidClientID(value string) bool {
	return len(value) <= 1024 && utf8.ValidString(value) && strings.TrimSpace(value) != "" &&
		!strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
