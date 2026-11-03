package cloud

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

	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/clientutil"
	"github.com/holon-sh/cli/pkg/oauthclient"
	"github.com/holon-sh/cli/pkg/protocol"
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

func newAPIClient(origin string, transport *http.Client, timeout time.Duration) (*apiClient, error) {
	base, err := protocol.CanonicalOrigin(origin)
	if err != nil {
		return nil, err
	}
	client := *cmp.Or(transport, http.DefaultClient)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &apiClient{baseURL: base, client: &client, timeout: cmp.Or(timeout, defaultHTTPTimeout)}, nil
}

func (c *apiClient) Do(ctx context.Context, method, path string, request apiRequest, result any) error {
	parsed, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || parsed.Fragment != "" || strings.HasPrefix(path, "//") ||
		parsed.RawPath != "" ||
		parsed.EscapedPath() != parsed.Path ||
		parsed.Path != pathpkg.Clean(parsed.Path) {
		return errInvalidRequestPath
	}
	timeout := c.timeout
	client := *c.client
	reqCtx := ctx
	if request.Stream == nil {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
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
	if request.Token != "" {
		req.Header.Set(protocol.HeaderAuthorization, protocol.BearerPrefix+request.Token)
	}
	response, err := client.Do(req)
	if err != nil {
		return cmp.Or(reqCtx.Err(), err)
	}
	defer response.Body.Close()
	if request.Stream != nil && response.StatusCode == http.StatusOK {
		mediaType, _, err := mime.ParseMediaType(response.Header.Get(protocol.HeaderContentType))
		if err != nil || mediaType != "text/plain" {
			return errMalformedResponse
		}
		// A log stream only ends while the command runs when the store's stream fails.
		if _, err = io.Copy(request.Stream, response.Body); err == nil {
			err = errStreamEnded
		}
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
		return &httpError{StatusCode: response.StatusCode, Code: code, Message: message}
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

func responseError(data []byte, token string, status int) (string, string) {
	var value struct {
		Error   protocol.Error `json:"error"`
		Code    string         `json:"code"`
		Message string         `json:"message"`
	}
	if json.Unmarshal(data, &value) != nil {
		return "", ""
	}
	if value.Error.Code == "" {
		value.Error = protocol.Error{Code: value.Code, Message: value.Message}
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
	if c.installationUID != "" && info.Installation.UID != c.installationUID {
		return protocol.Info{}, errors.New("installation identity changed")
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
	if !oauthclient.ValidClientID(oauth.CLIClientID) || len(oauth.AdminAudience) > maxInfoField ||
		err != nil || !audience.IsAbs() || strings.Contains(oauth.AdminAudience, "#") {
		return errMalformedResponse
	}
	issuer, _ := url.Parse(oauth.Issuer)
	if !oauthclient.HTTPSURL(oauth.Issuer) || strings.TrimSpace(oauth.Issuer) != oauth.Issuer ||
		issuer.RawQuery != "" || issuer.RawPath != "" {
		return errInvalidIssuer
	}
	return nil
}

var errStreamEnded = errors.New("the log stream ended")
