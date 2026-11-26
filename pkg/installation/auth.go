package installation

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
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
	"github.com/opencontainers/go-digest"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/protocol"
)

const (
	maxOAuthToken = 16 << 10
	maxStateBytes = 16 << 10
	accessSafety  = 30 * time.Second

	discoveryTimeout  = 10 * time.Second
	maxDiscoveryBytes = 64 << 10

	maxPollIntervalSeconds = int64(time.Hour / time.Second)

	privateDirPerm  = 0o700
	privateFilePerm = 0o600
)

var (
	errOAuthFailed              = errors.New("OAuth request failed")
	errNotAuthenticated         = errors.New("not signed in")
	errCredentialUnavailable    = errors.New("credential store unavailable")
	errCredentialChanged        = errors.New("credentials changed by another process")
	errEndpointChanged          = errors.New("authentication endpoint changed")
	errDeviceDenied             = errors.New("device authorization denied")
	errDeviceExpired            = errors.New("device authorization expired")
	errRefreshFailed            = errors.New("token refresh failed")
	errReauthenticationRequired = errors.New("sign-in expired")
	errNoRefreshToken           = errors.New("authorization did not return a refresh token")
)

// signedOut reports an error that only holon login clears.
func signedOut(err error) bool {
	return errors.Is(err, errReauthenticationRequired) || errors.Is(err, errNotAuthenticated) || errors.Is(err, errEndpointChanged)
}

// credentialsLost reports an error that retrying cannot clear, because this process must authenticate again.
func credentialsLost(err error) bool {
	return signedOut(err) || errors.Is(err, errCredentialChanged)
}

type ClientConfig struct {
	Env       []string
	Origin    string
	ConfigDir string
	Output    io.Writer
	// Interactive reports that a person reads Output, so a command may wait for them to sign in.
	Interactive    bool
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

type authState struct {
	OAuth   protocol.OAuthInfo       `json:"oauth"`
	Version string                   `json:"version"`
	Refresh string                   `json:"refresh"`
	Access  map[string]*oauth2.Token `json:"access,omitempty"`
}

func credentialTarget(info *protocol.OAuthInfo) protocol.OAuthInfo {
	return protocol.OAuthInfo{Issuer: info.Issuer, AdminAudience: info.AdminAudience, CLIClientID: info.CLIClientID}
}

type Client struct {
	hookSent    map[string]time.Time
	environment func(context.Context, string) (*oauth2.Token, error)
	output      io.Writer
	interactive bool

	mu        sync.Mutex
	state     authState
	statePath string
	client    *apiClient
	info      protocol.Info
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
	if manager.environment != nil && manager.info.OAuth == nil {
		return nil, errors.New("authentication from the environment requires an identity-enabled installation")
	}
	return manager, nil
}

func newLocalAuthManager(config ClientConfig) (*Client, error) {
	env := commandenv.Environ(config.Env)
	manager := &Client{
		state:  authState{Access: map[string]*oauth2.Token{}},
		output: cmp.Or(config.Output, io.Discard), interactive: config.Interactive, hookSent: map[string]time.Time{},
	}
	set := func(keys ...string) bool {
		return slices.ContainsFunc(env, func(entry string) bool {
			key, _, _ := strings.Cut(entry, "=")
			return slices.Contains(keys, key)
		})
	}
	if set("HOLON_CLIENT_ID", "HOLON_CLIENT_SECRET") {
		id, secret := commandenv.Value(env, "HOLON_CLIENT_ID"), commandenv.Value(env, "HOLON_CLIENT_SECRET")
		if !validToken(id) || !validToken(secret) {
			return nil, errors.New("service-account authentication requires HOLON_CLIENT_ID and HOLON_CLIENT_SECRET together")
		}
		credentials := clientcredentials.Config{ClientID: id, ClientSecret: secret, AuthStyle: oauth2.AuthStyleInHeader}
		manager.environment = func(ctx context.Context, audience string) (*oauth2.Token, error) {
			return manager.machineToken(ctx, credentials, audience)
		}
	}
	if set("HOLON_TOKEN") {
		token := commandenv.Value(env, "HOLON_TOKEN")
		if manager.environment != nil || !validToken(token) {
			return nil, errors.New("HOLON_TOKEN must be a bearer token, set without HOLON_CLIENT_ID and HOLON_CLIENT_SECRET")
		}
		manager.environment = func(context.Context, string) (*oauth2.Token, error) { return &oauth2.Token{AccessToken: token}, nil }
	}
	dir, err := clientConfigDir(config.ConfigDir)
	if err != nil {
		return nil, err
	}
	origin, err := resolveOrigin(config.Origin, dir, env)
	if err != nil {
		return nil, err
	}
	manager.client = newAPIClient(origin, config.HTTPClient, config.RequestTimeout)
	manager.statePath = filepath.Join(dir, "remotes", digest.FromBytes([]byte(manager.client.baseURL)).Encoded()+".tokens.json")
	return manager, nil
}

func (m *Client) loginInteractive(ctx context.Context) error {
	if m.environment != nil {
		return errors.New("credentials are supplied by the environment")
	}
	if m.info.OAuth == nil {
		_, err := fmt.Fprintln(m.output, "Connected to "+m.client.baseURL+" (authentication disabled)")
		return err
	}
	before, _ := m.readState()
	refresh, err := m.deviceToken(ctx)
	if err != nil {
		return err
	}
	state := authState{OAuth: credentialTarget(m.info.OAuth), Version: rand.Text(), Refresh: refresh}
	return m.withLock(ctx, func() error {
		if saved, _ := m.readState(); saved.Version != before.Version {
			return errCredentialChanged
		}
		if err := m.writeState(state); err != nil {
			return err
		}
		m.state = state
		return nil
	})
}

func (m *Client) token(ctx context.Context, audience string) (*oauth2.Token, error) {
	if err := m.loadCredentials(); err != nil {
		return nil, err
	}
	// A response may omit expires_in per [RFC 6749 §5.1]. Its zero Expiry fails this check, so the token is renewed.
	//
	// [RFC 6749 §5.1]: https://www.rfc-editor.org/rfc/rfc6749.html#section-5.1
	if cached := m.state.Access[audience]; cached != nil && time.Until(cached.Expiry) > accessSafety {
		return cached, nil
	}
	config, err := m.oauthConfig(ctx)
	if err != nil {
		return nil, err
	}
	token, err := m.renewToken(ctx, config, m.state.Refresh, audience)
	if err != nil {
		return nil, err
	}
	m.state.Refresh, token.RefreshToken = token.RefreshToken, ""
	m.state.Access = map[string]*oauth2.Token{audience: token}
	return token, m.writeState(m.state)
}

func (m *Client) readState() (authState, error) {
	data, err := readCredentialFile(m.statePath)
	if err != nil {
		return authState{}, err
	}
	var state authState
	if err := protocol.DecodeJSONFields(data, &state, "oauth"); err != nil {
		return authState{}, errReauthenticationRequired
	}
	if state.Version == "" || len(state.Version) > maxInfoField {
		return authState{}, errCredentialUnavailable
	}
	if err := validateInfo(protocol.Info{OAuth: &state.OAuth}); err != nil {
		return authState{}, errReauthenticationRequired
	}
	return state, nil
}

func (m *Client) loadCredentials() error {
	state, err := m.readState()
	if err != nil {
		return err
	}
	if m.state.Version != "" && m.state.Version != state.Version {
		return errCredentialChanged
	}
	if state.OAuth != credentialTarget(m.info.OAuth) {
		return errEndpointChanged
	}
	if !validToken(state.Refresh) {
		return errReauthenticationRequired
	}
	for _, token := range state.Access {
		if token == nil || !validOAuthToken(token) {
			return errReauthenticationRequired
		}
	}
	m.state = state
	return nil
}

func (m *Client) renewToken(ctx context.Context, config *oauth2.Config, refreshToken, audience string) (*oauth2.Token, error) {
	ctx = m.oauthContext(ctx)
	// TokenSource cannot name the resource of [RFC 8707 §2.2], so this overrides the grant of a client credentials
	// request, which the library allows. It still retains an omitted refresh token per [RFC 6749 §6].
	//
	// [RFC 8707 §2.2]: https://www.rfc-editor.org/rfc/rfc8707.html#section-2.2
	// [RFC 6749 §6]: https://www.rfc-editor.org/rfc/rfc6749.html#section-6
	refresh := clientcredentials.Config{
		ClientID: config.ClientID, TokenURL: config.Endpoint.TokenURL, AuthStyle: config.Endpoint.AuthStyle,
		EndpointParams: url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "resource": {audience}},
	}
	response, err := refresh.Token(ctx)
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
	if m.environment != nil {
		return errors.New("credentials are supplied by the environment; unset HOLON_TOKEN, HOLON_CLIENT_ID and HOLON_CLIENT_SECRET")
	}
	return m.withLock(ctx, func() error {
		revokeErr := m.revokeRefreshTokens(ctx)
		if err := removeCredentialFile(m.statePath); err != nil {
			return errors.Join(err, revokeErr)
		}
		if revokeErr != nil {
			return fmt.Errorf("credentials removed locally, but refresh tokens may still be valid at the identity provider: %w", revokeErr)
		}
		return nil
	})
}

// Revocation follows [RFC 7009 §2.1] with the public client's identifier.
//
// [RFC 7009 §2.1]: https://www.rfc-editor.org/rfc/rfc7009.html#section-2.1
func (m *Client) revokeRefreshTokens(ctx context.Context) error {
	state, err := m.readState()
	if errors.Is(err, errNotAuthenticated) {
		return nil
	}
	if err != nil {
		return err
	}
	_, revocation, err := m.discover(ctx, state.OAuth.Issuer, state.OAuth.CLIClientID)
	if err != nil {
		return err
	}
	return m.revoke(ctx, revocation, state.OAuth.CLIClientID, state.Refresh)
}

func (m *Client) revoke(ctx context.Context, endpoint, clientID, token string) error {
	if endpoint == "" {
		return fmt.Errorf("%w: issuer does not advertise a revocation endpoint", errOAuthFailed)
	}
	form := url.Values{"token": {token}, "token_type_hint": {"refresh_token"}, "client_id": {clientID}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The response body is ignored per [RFC 7009 §2.2], and could echo the token.
	//
	// [RFC 7009 §2.2]: https://www.rfc-editor.org/rfc/rfc7009.html#section-2.2
	status, _, err := m.identityRequest(request)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("%w: HTTP %d", errOAuthFailed, status)
	}
	return err
}

// discover reads the issuer's metadata at the [OIDC Discovery §4] path, which differs from RFC 8414 for path issuers,
// and returns its revocation endpoint beside the client configuration.
//
// [OIDC Discovery §4]: https://openid.net/specs/openid-connect-discovery-1_0.html#ProviderConfig
func (m *Client) discover(ctx context.Context, issuer, clientID string) (*oauth2.Config, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, "", fmt.Errorf("OIDC discovery: %w", errOAuthFailed)
	}
	var metadata struct {
		Issuer     string `json:"issuer"`
		Token      string `json:"token_endpoint"`
		Device     string `json:"device_authorization_endpoint"`
		Revocation string `json:"revocation_endpoint"`
	}
	status, body, err := m.identityRequest(request)
	if err != nil || status != http.StatusOK || json.Unmarshal(body, &metadata) != nil || metadata.Issuer != issuer {
		return nil, "", cmp.Or(ctx.Err(), fmt.Errorf("OIDC discovery: %w", errOAuthFailed))
	}
	if !httpsURL(metadata.Token) || metadata.Device != "" && !httpsURL(metadata.Device) || metadata.Revocation != "" && !httpsURL(metadata.Revocation) {
		return nil, "", errMalformedResponse
	}
	return &oauth2.Config{
		ClientID: clientID,
		Scopes:   []string{"openid", "offline_access"},
		Endpoint: oauth2.Endpoint{TokenURL: metadata.Token, DeviceAuthURL: metadata.Device, AuthStyle: oauth2.AuthStyleInParams},
	}, metadata.Revocation, nil
}

func (m *Client) oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	config, _, err := m.discover(ctx, m.info.OAuth.Issuer, m.info.OAuth.CLIClientID)
	return config, err
}

// identityRequest sends request to the identity provider and reads at most maxDiscoveryBytes of the response.
func (m *Client) identityRequest(request *http.Request) (int, []byte, error) {
	client := *m.client.client
	client.Timeout = discoveryTimeout
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := clientutil.ReadLimited(response.Body, maxDiscoveryBytes)
	return response.StatusCode, body, err
}

func (m *Client) oauthContext(ctx context.Context) context.Context {
	client := *m.client.client
	client.Timeout = m.client.timeout
	return context.WithValue(ctx, oauth2.HTTPClient, &client)
}

// validOAuthToken checks the parameters of a successful response per [RFC 6749 §5.1]: access_token and a
// case-insensitive token_type are required, refresh_token is optional, and expires_in is only recommended.
//
// [RFC 6749 §5.1]: https://www.rfc-editor.org/rfc/rfc6749.html#section-5.1
func validOAuthToken(token *oauth2.Token) bool {
	return validToken(token.AccessToken) && strings.EqualFold(token.TokenType, "Bearer") &&
		(token.RefreshToken == "" || validToken(token.RefreshToken))
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
	if err != nil {
		return nil, errCredentialUnavailable
	}
	return data, nil
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
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

func (m *Client) deviceToken(ctx context.Context) (string, error) {
	config, err := m.oauthConfig(ctx)
	if err != nil {
		return "", err
	}
	if config.Endpoint.DeviceAuthURL == "" {
		return "", fmt.Errorf("OIDC discovery: %w: issuer does not advertise a device authorization endpoint", errOAuthFailed)
	}
	device, err := config.DeviceAuth(m.oauthContext(ctx))
	if err != nil {
		return "", fmt.Errorf("device authorization: %w", oauthFailure(ctx, err))
	}
	validUserCode := device.UserCode != "" && len(device.UserCode) <= 512 &&
		!strings.ContainsFunc(device.UserCode, func(r rune) bool { return r < 0x20 || r == 0x7f })
	if !httpsURL(device.VerificationURI) || !validUserCode || !validToken(device.DeviceCode) ||
		device.Interval < 0 || device.Interval > maxPollIntervalSeconds {
		return "", errMalformedResponse
	}
	if _, err := fmt.Fprintf(m.output, "Open %s\nCode: %s\n", device.VerificationURI, device.UserCode); err != nil {
		return "", err
	}
	tokens, err := config.DeviceAccessToken(m.oauthContext(ctx), device)
	if err != nil {
		return "", fmt.Errorf("device token polling: %w", oauthFailure(ctx, err))
	}
	if !validToken(tokens.RefreshToken) {
		return "", errNoRefreshToken
	}
	return tokens.RefreshToken, nil
}

func (m *Client) controlToken(ctx context.Context) (string, error) {
	if m.info.OAuth == nil {
		return "", nil
	}
	token, err := m.resourceToken(ctx, m.info.OAuth.AdminAudience)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func (m *Client) resourceToken(ctx context.Context, audience string) (*oauth2.Token, error) {
	if m.environment != nil {
		return m.environment(ctx, audience)
	}
	var result *oauth2.Token
	token := func() error {
		return m.withLock(ctx, func() (err error) {
			result, err = m.token(ctx, audience)
			return err
		})
	}
	err := token()
	if signedOut(err) && m.interactive {
		if err := m.loginInteractive(ctx); err != nil {
			return nil, err
		}
		err = token()
	}
	if signedOut(err) {
		return nil, fmt.Errorf("%w; run holon login %s", err, m.client.baseURL)
	}
	return result, err
}

func (m *Client) machineToken(ctx context.Context, credentials clientcredentials.Config, audience string) (*oauth2.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A response may omit expires_in per [RFC 6749 §5.1]. Its zero Expiry fails this check, so the token is renewed.
	//
	// [RFC 6749 §5.1]: https://www.rfc-editor.org/rfc/rfc6749.html#section-5.1
	if cached := m.state.Access[audience]; cached != nil && time.Until(cached.Expiry) > accessSafety {
		return cached, nil
	}
	config, err := m.oauthConfig(ctx)
	if err != nil {
		return nil, err
	}
	credentials.TokenURL = config.Endpoint.TokenURL
	credentials.EndpointParams = url.Values{"resource": {audience}}
	token, err := credentials.Token(m.oauthContext(ctx))
	if err != nil {
		return nil, cmp.Or(ctx.Err(), errOAuthFailed)
	}
	if !validOAuthToken(token) {
		return nil, errMalformedResponse
	}
	m.state.Access[audience] = token
	return token, nil
}

func (m *Client) Call(ctx context.Context, host protocol.Host, tool string, input, output any) error {
	token, err := m.controlToken(ctx)
	if err != nil {
		return err
	}
	return m.client.Call(ctx, host, token, tool, input, output)
}

func (config ClientConfig) AppToken(ctx context.Context, resource string) (string, error) {
	local, err := newLocalAuthManager(config)
	if err != nil {
		return "", err
	}
	endpoint, err := url.Parse(resource)
	if err != nil {
		return "", err
	}
	if _, _, ok := protocol.ParseSessionAudience(local.client.baseURL, endpoint.Scheme+"://"+endpoint.Host); !ok {
		return "", nil
	}
	manager, err := NewClient(ctx, config)
	if err != nil || manager.info.OAuth == nil {
		return "", err
	}
	token, err := manager.resourceToken(ctx, resource)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}
