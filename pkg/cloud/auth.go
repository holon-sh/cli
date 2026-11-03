package cloud

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	oidcclient "github.com/zitadel/oidc/v3/pkg/client"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/commandenv"
	"github.com/holon-sh/cli/pkg/oauthclient"
	"github.com/holon-sh/cli/pkg/protocol"
)

const (
	maxOAuthToken = 16 << 10
	maxStateBytes = 16 << 10
	accessSafety  = 30 * time.Second

	maxTokenLifetimeSeconds = int64(24 * time.Hour / time.Second)
	maxPollIntervalSeconds  = int64(time.Hour / time.Second)

	privateDirPerm  = 0o700
	privateFilePerm = 0o600
)

var (
	errOAuthFailed              = errors.New("OAuth request failed")
	errNotAuthenticated         = errors.New("not authenticated")
	errCredentialUnavailable    = errors.New("credential store unavailable")
	errCredentialChanged        = errors.New("credentials changed by another process")
	errEndpointChanged          = errors.New("authentication endpoint changed")
	errDeviceDenied             = errors.New("device authorization denied")
	errDeviceExpired            = errors.New("device authorization expired")
	errRefreshFailed            = errors.New("token refresh failed")
	errReauthenticationRequired = errors.New("reauthentication required; rerun the command")
	errNoRefreshToken           = errors.New("authorization did not return a refresh token")
)

type ClientConfig struct {
	Env            []string
	Origin         string
	ConfigDir      string
	Output         io.Writer
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	now            func() time.Time
}

type authState struct {
	OAuth   *credentialTarget        `json:"oauth"`
	Version string                   `json:"version"`
	Tokens  map[string]*oauth2.Token `json:"tokens,omitempty"`
}

type credentialTarget struct {
	Issuer        string `json:"issuer"`
	AdminAudience string `json:"adminAudience"`
	CLIClientID   string `json:"cliClientID"`
}

func targetForOAuth(info *protocol.OAuthInfo) *credentialTarget {
	if info == nil {
		return nil
	}
	return &credentialTarget{Issuer: info.Issuer, AdminAudience: info.AdminAudience, CLIClientID: info.CLIClientID}
}

type Client struct {
	hookMu   sync.Mutex
	hookSent map[string]time.Time
	machine  *clientcredentials.Config
	output   io.Writer

	mu        sync.Mutex
	state     authState
	statePath string
	client    *apiClient
	info      protocol.Info
	now       func() time.Time
}

func NewClient(ctx context.Context, config ClientConfig) (*Client, error) {
	manager, err := newLocalAuthManager(config)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(manager.output, "Running against %s\n", manager.client.baseURL); err != nil {
		return nil, err
	}
	manager.info, err = manager.client.Info(ctx)
	if err != nil {
		return nil, err
	}
	manager.client.installationUID = manager.info.Installation.UID
	if manager.machine != nil && manager.info.OAuth == nil {
		return nil, errors.New("service-account authentication requires an identity-enabled installation")
	}
	return manager, nil
}

func newLocalAuthManager(config ClientConfig) (*Client, error) {
	env, err := commandenv.Environ(config.Env)
	if err != nil {
		return nil, err
	}
	if config.now == nil {
		config.now = time.Now
	}
	manager := &Client{
		now: config.now, state: authState{Tokens: map[string]*oauth2.Token{}},
		output: cmp.Or(config.Output, io.Discard), hookSent: map[string]time.Time{},
	}
	if slices.ContainsFunc(env, func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return slices.Contains([]string{"HOLON_CLIENT_ID", "HOLON_CLIENT_SECRET"}, key)
	}) {
		id, secret := commandenv.Value(env, "HOLON_CLIENT_ID"), commandenv.Value(env, "HOLON_CLIENT_SECRET")
		if !validToken(id) || !validToken(secret) {
			return nil, errors.New("service-account authentication requires HOLON_CLIENT_ID and HOLON_CLIENT_SECRET together")
		}
		manager.machine = &clientcredentials.Config{
			ClientID: id, ClientSecret: secret, AuthStyle: oauth2.AuthStyleInHeader,
		}
	}
	dir, err := clientConfigDir(config.ConfigDir)
	if err != nil {
		return nil, err
	}
	origin, err := resolveOrigin(config.Origin, dir, env)
	if err != nil {
		return nil, err
	}
	manager.client, err = newAPIClient(origin, config.HTTPClient, config.RequestTimeout)
	if err != nil {
		return nil, err
	}
	account := fmt.Sprintf("%x", sha256.Sum256([]byte(manager.client.baseURL)))
	manager.statePath = filepath.Join(dir, "remotes", account+".tokens.json")
	return manager, err
}

func (m *Client) loginInteractive(ctx context.Context) error {
	if m.machine != nil {
		return errors.New("service-account credentials are supplied by the environment")
	}
	return m.withLock(ctx, func() error {
		return m.login(ctx, m.audience(protocol.AdminPlane), protocol.SystemScopes())
	})
}

func (m *Client) login(ctx context.Context, audience string, scopes []string) error {
	state := authState{OAuth: targetForOAuth(m.info.OAuth), Version: rand.Text(), Tokens: map[string]*oauth2.Token{}}
	if m.info.OAuth != nil {
		config, err := m.oauthConfig(ctx, scopes)
		if err != nil {
			return err
		}
		tokens, err := m.deviceToken(ctx, config, audience)
		if err != nil {
			return err
		}
		tokens.Expiry = m.now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		state.Tokens[tokenKey(m.info.OAuth.Issuer, audience, scopes)] = tokens
	}
	if err := m.writeState(state); err != nil {
		return err
	}
	m.state = state
	if m.info.OAuth == nil {
		_, err := fmt.Fprintln(m.output, "Connected to "+m.client.baseURL+" (authentication disabled)")
		return err
	}
	return nil
}

func tokenKey(issuer, audience string, scopes []string) string {
	scopes = slices.Compact(slices.Sorted(slices.Values(scopes)))
	return issuer + "\x00" + audience + "\x00" + strings.Join(scopes, " ")
}

func (m *Client) audience(plane protocol.Plane) string {
	audience, _ := protocol.PlaneOrigin(m.client.baseURL, plane, "")
	return audience
}

func (m *Client) token(ctx context.Context, audience string, scopes []string) (*oauth2.Token, error) {
	if err := m.loadCredentials(); err != nil {
		return nil, err
	}
	key := tokenKey(m.info.OAuth.Issuer, audience, scopes)
	cached := ptr.Deref(m.state.Tokens[key], oauth2.Token{})
	if cached.Expiry.After(m.now().Add(accessSafety)) {
		return &cached, nil
	}
	config, err := m.oauthConfig(ctx, scopes)
	if err != nil {
		return nil, err
	}
	var token *oauth2.Token
	if m.machine != nil || cached.RefreshToken != "" {
		token, err = m.renewToken(ctx, config, audience, cached.RefreshToken)
	} else {
		token, err = m.deviceToken(ctx, config, audience)
	}
	if err != nil {
		return nil, err
	}
	token.Expiry = m.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	m.state.Tokens[key] = token
	return token, m.writeState(m.state)
}

func (m *Client) loadCredentials() error {
	if m.machine != nil {
		return nil
	}
	data, err := readCredentialFile(m.statePath)
	if err != nil {
		return err
	}
	var state authState
	if err := protocol.DecodeJSONFields(data, &state, "oauth"); err != nil {
		return errReauthenticationRequired
	}
	if state.Version == "" || len(state.Version) > maxInfoField {
		return errCredentialUnavailable
	}
	if state.OAuth != nil {
		if err := validateInfo(protocol.Info{OAuth: &protocol.OAuthInfo{
			Issuer: state.OAuth.Issuer, AdminAudience: state.OAuth.AdminAudience, CLIClientID: state.OAuth.CLIClientID,
		}}); err != nil {
			return errReauthenticationRequired
		}
	}
	if m.state.Version != "" && m.state.Version != state.Version {
		return errCredentialChanged
	}
	if !ptr.Equal(state.OAuth, targetForOAuth(m.info.OAuth)) {
		return errEndpointChanged
	}
	if len(state.Tokens) == 0 {
		return errReauthenticationRequired
	}
	for key, token := range state.Tokens {
		if !strings.HasPrefix(key, m.info.OAuth.Issuer+"\x00") || token == nil || !validOAuthToken(token) ||
			token.RefreshToken == "" || token.Expiry.IsZero() {
			return errReauthenticationRequired
		}
	}
	m.state = state
	return nil
}

func (m *Client) renewToken(ctx context.Context, config *oauth2.Config, audience, refreshToken string) (*oauth2.Token, error) {
	ctx = m.oauthContext(ctx)
	if m.machine != nil {
		credentials := *m.machine
		credentials.Scopes, credentials.TokenURL = config.Scopes, config.Endpoint.TokenURL
		credentials.EndpointParams = url.Values{"resource": {audience}}
		token, err := credentials.Token(ctx)
		if err != nil {
			return nil, cmp.Or(ctx.Err(), errOAuthFailed)
		}
		token.ExpiresIn = int64((time.Until(token.Expiry) + time.Second - 1) / time.Second)
		if !validOAuthToken(token) {
			return nil, errMalformedResponse
		}
		return token, nil
	}
	// TokenSource retains an omitted refresh token per [RFC 6749 §6].
	//
	// [RFC 6749 §6]: https://www.rfc-editor.org/rfc/rfc6749.html#section-6
	response, err := config.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil || !validOAuthToken(response) {
		failure := oauthFailure(ctx, err)
		if ctx.Err() != nil || errors.Is(failure, errReauthenticationRequired) {
			return nil, failure
		}
		return nil, errRefreshFailed
	}
	return response, nil
}

func (m *Client) logout(ctx context.Context) error {
	if m.machine != nil {
		return errors.New("service-account credentials are supplied by the environment; unset HOLON_CLIENT_ID and HOLON_CLIENT_SECRET")
	}
	return m.withLock(ctx, func() error { return removeCredentialFile(m.statePath) })
}

func (m *Client) oauthConfig(ctx context.Context, scopes []string) (*oauth2.Config, error) {
	client, _ := oauthclient.HTTPClient("", m.client.client)
	// This library uses [OIDC Discovery §4], whose well-known path differs from RFC 8414 for path issuers.
	//
	// [OIDC Discovery §4]: https://openid.net/specs/openid-connect-discovery-1_0.html#ProviderConfig
	metadata, err := oidcclient.Discover(ctx, m.info.OAuth.Issuer, client)
	if err != nil {
		return nil, cmp.Or(ctx.Err(), fmt.Errorf("OIDC discovery: %w", errOAuthFailed))
	}
	if !oauthclient.HTTPSURL(metadata.TokenEndpoint) ||
		metadata.DeviceAuthorizationEndpoint != "" && !oauthclient.HTTPSURL(metadata.DeviceAuthorizationEndpoint) {
		return nil, errMalformedResponse
	}
	if m.machine == nil {
		scopes = append([]string{"openid", "offline_access"}, scopes...)
	}
	return &oauth2.Config{
		ClientID: m.info.OAuth.CLIClientID, Scopes: scopes,
		Endpoint: oauth2.Endpoint{TokenURL: metadata.TokenEndpoint, DeviceAuthURL: metadata.DeviceAuthorizationEndpoint, AuthStyle: oauth2.AuthStyleInParams},
	}, nil
}

func (m *Client) oauthContext(ctx context.Context) context.Context {
	client := *m.client.client
	client.Timeout = m.client.timeout
	return context.WithValue(ctx, oauth2.HTTPClient, &client)
}

func validOAuthToken(token *oauth2.Token) bool {
	return validToken(token.AccessToken) && strings.EqualFold(token.TokenType, "Bearer") &&
		token.ExpiresIn > 0 && token.ExpiresIn <= maxTokenLifetimeSeconds && (token.RefreshToken == "" || validToken(token.RefreshToken))
}

func oauthFailure(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errDeviceExpired
	}
	if failure, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		_ = failure.Response.Body.Close()
		switch failure.ErrorCode {
		case "access_denied":
			return errDeviceDenied
		case "expired_token":
			return errDeviceExpired
		case "invalid_grant":
			return errReauthenticationRequired
		}
		if failure.Response.StatusCode == http.StatusUnauthorized {
			return errReauthenticationRequired
		}
		return fmt.Errorf("%w: HTTP %d", errOAuthFailed, failure.Response.StatusCode)
	}
	return errOAuthFailed
}

func validToken(value string) bool {
	if value == "" || len(value) > maxOAuthToken || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool { return r < 0x21 || r == 0x7f })
}

func (m *Client) withLock(ctx context.Context, fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.machine != nil {
		return fn()
	}
	if err := os.MkdirAll(filepath.Dir(m.statePath), privateDirPerm); err != nil {
		return errCredentialUnavailable
	}
	lock := flock.New(m.statePath+".lock", flock.SetPermissions(privateFilePerm))
	if locked, _ := lock.TryLockContext(ctx, 25*time.Millisecond); !locked {
		return cmp.Or(ctx.Err(), errCredentialUnavailable)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNotAuthenticated
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return nil, errCredentialUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxStateBytes {
		return nil, errCredentialUnavailable
	}
	return data, nil
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if len(data) > maxStateBytes {
		return errCredentialUnavailable
	}
	if err := os.MkdirAll(dir, privateDirPerm); err != nil {
		return errCredentialUnavailable
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errCredentialUnavailable
	}
	defer func() { _ = root.Close() }()
	return writeAtomicFile(root, filepath.Base(path), data)
}

func (m *Client) deviceToken(ctx context.Context, config *oauth2.Config, audience string) (*oauth2.Token, error) {
	if config.Endpoint.DeviceAuthURL == "" {
		return nil, fmt.Errorf("OIDC discovery: %w: issuer does not advertise a device authorization endpoint", errOAuthFailed)
	}
	options := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("resource", audience)}
	device, err := config.DeviceAuth(m.oauthContext(ctx), options...)
	if err != nil {
		return nil, fmt.Errorf("device authorization: %w", oauthFailure(ctx, err))
	}
	validUserCode := device.UserCode != "" && len(device.UserCode) <= 512 &&
		!strings.ContainsFunc(device.UserCode, func(r rune) bool { return r < 0x20 || r == 0x7f })
	if !oauthclient.HTTPSURL(device.VerificationURI) || !validUserCode || !validToken(device.DeviceCode) ||
		time.Until(device.Expiry) <= 0 || time.Until(device.Expiry) > 24*time.Hour ||
		device.Interval < 0 || device.Interval > maxPollIntervalSeconds {
		return nil, errMalformedResponse
	}
	if _, err := fmt.Fprintf(m.output, "Open %s\nCode: %s\n", device.VerificationURI, device.UserCode); err != nil {
		return nil, err
	}
	tokens, err := config.DeviceAccessToken(m.oauthContext(ctx), device, options...)
	if err != nil {
		return nil, fmt.Errorf("device token polling: %w", oauthFailure(ctx, err))
	}
	if !validOAuthToken(tokens) {
		return nil, errMalformedResponse
	}
	if tokens.RefreshToken == "" {
		return nil, errNoRefreshToken
	}
	return tokens, nil
}

func (m *Client) controlToken(ctx context.Context, plane protocol.Plane) (string, error) {
	if plane != protocol.AdminPlane && plane != protocol.DevPlane {
		return "", errInvalidRequestPath
	}
	token, err := m.resourceToken(ctx, m.audience(plane), protocol.ControlScopes(plane))
	if err != nil || token == nil {
		return "", err
	}
	return token.AccessToken, nil
}

func (m *Client) resourceToken(ctx context.Context, audience string, scopes []string) (*oauth2.Token, error) {
	if m.info.OAuth == nil {
		return nil, nil
	}
	var result *oauth2.Token
	err := m.withLock(ctx, func() error {
		initial := m.state.Version == ""
		var err error
		result, err = m.token(ctx, audience, scopes)
		if initial && m.machine == nil &&
			(errors.Is(err, errNotAuthenticated) || errors.Is(err, errEndpointChanged) || errors.Is(err, errReauthenticationRequired)) {
			err = m.login(ctx, audience, scopes)
			result = m.state.Tokens[tokenKey(m.info.OAuth.Issuer, audience, scopes)]
		}
		return err
	})
	return result, err
}

func (m *Client) Call(ctx context.Context, plane protocol.Plane, tool string, input, output any) error {
	token, err := m.controlToken(ctx, plane)
	if err != nil {
		return err
	}
	return m.client.Call(ctx, plane, token, tool, input, output)
}

func (m *Client) Issuer() string {
	return ptr.Deref(m.info.OAuth, protocol.OAuthInfo{}).Issuer
}
