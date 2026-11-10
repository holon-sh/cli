package installation

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/protocol"
)

const (
	defaultHTTPTimeout = 15 * time.Second
	maxHTTPBody        = 512 << 10
	maxRequestBody     = 64 << 10
	maxInfoField       = 4096
)

var (
	errInvalidIssuer      = errors.New("invalid issuer")
	errMalformedResponse  = errors.New("malformed response")
	errResponseTooLarge   = errors.New("response too large")
	errInvalidRequestPath = errors.New("invalid request path")
)

type apiRequest struct {
	Plane  protocol.Plane
	Token  string
	Stream io.Writer
}

type httpError struct {
	StatusCode int
	Code       string
	Message    string
	MovedTo    string
}

func (e *httpError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("control API request returned status %d", e.StatusCode)
	}
	if e.Message != "" {
		return fmt.Sprintf("control API request returned status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("control API request returned status %d (%s)", e.StatusCode, e.Code)
}

type apiClient struct {
	baseURL         string
	installationUID string
	client          *http.Client
	timeout         time.Duration
}

func newAPIClient(origin string, transport *http.Client, timeout time.Duration) *apiClient {
	client := *cmp.Or(transport, http.DefaultClient)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &apiClient{baseURL: origin, client: &client, timeout: cmp.Or(timeout, defaultHTTPTimeout)}
}

func (c *apiClient) Do(ctx context.Context, method, path string, request apiRequest, result any) error {
	parsed, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || parsed.Fragment != "" || strings.HasPrefix(path, "//") ||
		parsed.RawPath != "" ||
		parsed.EscapedPath() != parsed.Path ||
		parsed.Path != pathpkg.Clean(parsed.Path) {
		return errInvalidRequestPath
	}
	client := *c.client
	reqCtx := ctx
	if request.Stream == nil {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	} else {
		client.Timeout = 0
	}
	origin, _ := protocol.PlaneOrigin(c.baseURL, request.Plane, "")
	req, err := http.NewRequestWithContext(reqCtx, method, origin+path, nil)
	if err != nil {
		return errInvalidRequestPath
	}
	if c.installationUID != "" {
		req.Header.Set(protocol.HeaderInstallationUID, c.installationUID)
	}
	response, err := client.Do(clientutil.Bearer(req, origin, request.Token))
	if err != nil {
		return cmp.Or(reqCtx.Err(), err)
	}
	defer response.Body.Close()
	if request.Stream != nil && response.StatusCode == http.StatusOK {
		mediaType, _, err := mime.ParseMediaType(response.Header.Get(protocol.HeaderContentType))
		if err != nil || mediaType != "text/plain" {
			return errMalformedResponse
		}
		_, err = io.Copy(request.Stream, response.Body)
		return err
	}
	data, err := clientutil.ReadLimited(response.Body, maxHTTPBody)
	if errors.Is(err, clientutil.ErrContentTooLarge) {
		return errResponseTooLarge
	}
	if err != nil {
		return errMalformedResponse
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		code, message := responseError(data, request.Token, response.StatusCode)
		failure := &httpError{StatusCode: response.StatusCode, Code: code, Message: message}
		if location, err := response.Location(); err == nil && location.Host != req.URL.Host &&
			(response.StatusCode == http.StatusMovedPermanently || response.StatusCode == http.StatusPermanentRedirect) {
			failure.MovedTo, _ = protocol.CanonicalOrigin("https://" + location.Host)
		}
		return failure
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			return errMalformedResponse
		}
	}
	return nil
}

func HTTPStatus(err error) int {
	if failure, ok := errors.AsType[*protocol.Error](err); ok {
		switch failure.Code {
		case "not_found":
			return http.StatusNotFound
		case "forbidden", "insufficient_scope":
			return http.StatusForbidden
		case "unauthorized":
			return http.StatusUnauthorized
		case "invalid_request":
			return http.StatusBadRequest
		case "conflict", "baseline_unknown", "identity_required_by_apps":
			return http.StatusConflict
		case "precondition_failed":
			return http.StatusPreconditionFailed
		case "precondition_required":
			return http.StatusPreconditionRequired
		default:
			return http.StatusServiceUnavailable
		}
	}
	failure, _ := errors.AsType[*httpError](err)
	return ptr.Deref(failure, httpError{}).StatusCode
}

// resumable reports whether a log stream failure is transient: the stream ended, its transport failed or the server
// returned a 5xx. A rejected or malformed response, including an installation mismatch, is final.
func resumable(err error) bool {
	if status := HTTPStatus(err); status != 0 {
		return status >= http.StatusInternalServerError
	}
	return !errors.Is(err, errMalformedResponse) && !errors.Is(err, errResponseTooLarge) && !errors.Is(err, errInvalidRequestPath)
}

func responseError(data []byte, token string, status int) (string, string) {
	var value protocol.ErrorResponse
	if json.Unmarshal(data, &value) != nil {
		return "", ""
	}
	if value.Error.Code == "" || len(value.Error.Code) > 128 || strings.Trim(value.Error.Code, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return "", ""
	}
	message := value.Error.Message
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError || len(message) > 256 ||
		strings.ContainsFunc(message, unicode.IsControl) || token != "" && strings.Contains(message, token) {
		message = ""
	}
	return value.Error.Code, message
}

func (c *apiClient) Info(ctx context.Context) (protocol.Info, error) {
	var info protocol.Info
	if err := c.Do(ctx, http.MethodGet, protocol.APIPathInfo, apiRequest{Plane: protocol.BasePlane}, &info); err != nil {
		if failure, ok := errors.AsType[*httpError](err); ok && failure.MovedTo != "" {
			return protocol.Info{}, fmt.Errorf("this installation moved to %s; run holon login %s", failure.MovedTo, failure.MovedTo)
		}
		return protocol.Info{}, err
	}
	if err := validateInfo(info); err != nil {
		return protocol.Info{}, err
	}
	if info.Installation.Origin != c.baseURL || info.Installation.Validate() != nil {
		return protocol.Info{}, errMalformedResponse
	}
	expectedAdmin, _ := protocol.PlaneOrigin(c.baseURL, protocol.AdminPlane, "")
	if info.OAuth != nil && info.OAuth.AdminAudience != expectedAdmin {
		return protocol.Info{}, errMalformedResponse
	}
	return info, nil
}

func validateInfo(info protocol.Info) error {
	if len(info.Version) > maxInfoField || !protocol.ValidImageRepositoryPrefix(info.ImageRepositoryPrefix) ||
		!protocol.ValidBuildArchitectures(info.BuildArchitectures) {
		return errMalformedResponse
	}
	if info.OAuth == nil {
		return nil
	}
	oauth := info.OAuth
	audience, err := url.Parse(oauth.AdminAudience)
	if !validClientID(oauth.CLIClientID) || len(oauth.AdminAudience) > maxInfoField ||
		err != nil || !audience.IsAbs() || strings.Contains(oauth.AdminAudience, "#") {
		return errMalformedResponse
	}
	issuer, _ := url.Parse(oauth.Issuer)
	if !httpsURL(oauth.Issuer) || strings.TrimSpace(oauth.Issuer) != oauth.Issuer ||
		issuer.RawQuery != "" || issuer.RawPath != "" {
		return errInvalidIssuer
	}
	return nil
}

func httpsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func validClientID(value string) bool {
	return len(value) <= 1024 && utf8.ValidString(value) && strings.TrimSpace(value) != "" &&
		!strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
