package installation

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
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
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"k8s.io/utils/ptr"

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
	errNoAudienceToken          = errors.New("this session needs its own sign-in, which only runs at a terminal")
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
	hookSent    map[string]time.Time
	machine     *clientcredentials.Config
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
	manager := &Client{
		state:  authState{Tokens: map[string]*oauth2.Token{}},
		output: cmp.Or(config.Output, io.Discard), interactive: config.Interactive, hookSent: map[string]time.Time{},
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
	manager.client = newAPIClient(origin, config.HTTPClient, config.RequestTimeout)
	manager.statePath = filepath.Join(dir, "remotes", digest.FromBytes([]byte(manager.client.baseURL)).Encoded()+".tokens.json")
	return manager, nil
}

// loginInteractive waits for the device sign-in without the credentials lock, so other commands never wait on a person.
func (m *Client) loginInteractive(ctx context.Context) error {
	if m.machine != nil {
		return errors.New("service-account credentials are supplied by the environment")
	}
	state := authState{OAuth: targetForOAuth(m.info.OAuth), Version: rand.Text(), Tokens: map[string]*oauth2.Token{}}
	if m.info.OAuth != nil {
		tokens, err := m.deviceToken(ctx, m.info.OAuth.AdminAudience)
		if err != nil {
			return err
		}
		state.Tokens[m.info.OAuth.AdminAudience] = tokens
	}
	return m.withLock(ctx, func() error {
		if err := m.writeState(state); err != nil {
			return err
		}
		m.state = state
		if m.info.OAuth == nil {
			_, err := fmt.Fprintln(m.output, "Connected to "+m.client.baseURL+" (authentication disabled)")
			return err
		}
		return nil
	})
}

func (m *Client) token(ctx context.Context, audience string) (*oauth2.Token, error) {
	if err := m.loadCredentials(); err != nil {
		return nil, err
	}
	if m.state.Tokens[audience] == nil {
		return nil, errNoAudienceToken
	}
	cached := *m.state.Tokens[audience]
	// A response may omit expires_in per [RFC 6749 §5.1]. Its zero Expiry fails this check, so the token is renewed.
	//
	// [RFC 6749 §5.1]: https://www.rfc-editor.org/rfc/rfc6749.html#section-5.1
	if time.Until(cached.Expiry) > accessSafety {
		return &cached, nil
	}
	config, err := m.oauthConfig(ctx)
	if err != nil {
		return nil, err
	}
	token, err := m.renewToken(ctx, config, cached.RefreshToken, audience)
	if err != nil {
		return nil, err
	}
	m.state.Tokens[audience] = token
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
	if state.OAuth != nil {
		if err := validateInfo(protocol.Info{OAuth: &protocol.OAuthInfo{
			Issuer: state.OAuth.Issuer, AdminAudience: state.OAuth.AdminAudience, CLIClientID: state.OAuth.CLIClientID,
		}}); err != nil {
			return authState{}, errReauthenticationRequired
		}
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
	if !ptr.Equal(state.OAuth, targetForOAuth(m.info.OAuth)) {
		return errEndpointChanged
	}
	if len(state.Tokens) == 0 {
		return errReauthenticationRequired
	}
	for _, token := range state.Tokens {
		if token == nil || !validOAuthToken(token) || token.RefreshToken == "" {
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
	if m.machine != nil {
		return errors.New("service-account credentials are supplied by the environment; unset HOLON_CLIENT_ID and HOLON_CLIENT_SECRET")
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
	if err != nil && !errors.Is(err, errNotAuthenticated) {
		return err
	}
	if state.OAuth == nil {
		return nil
	}
	party, err := m.relyingParty(ctx, state.OAuth.Issuer, state.OAuth.CLIClientID)
	if err != nil {
		return err
	}
	for _, token := range state.Tokens {
		err = errors.Join(err, rp.RevokeToken(ctx, party, ptr.Deref(token, oauth2.Token{}).RefreshToken, "refresh_token"))
	}
	return err
}

func (m *Client) relyingParty(ctx context.Context, issuer, clientID string) (rp.RelyingParty, error) {
	client := *m.client.client
	client.Timeout = discoveryTimeout
	client.Transport = boundedTransport{base: cmp.Or(client.Transport, http.DefaultTransport)}
	// This library uses [OIDC Discovery §4], whose well-known path differs from RFC 8414 for path issuers.
	//
	// [OIDC Discovery §4]: https://openid.net/specs/openid-connect-discovery-1_0.html#ProviderConfig
	party, err := rp.NewRelyingPartyOIDC(ctx, issuer, clientID, "", "", []string{"openid", "offline_access"}, rp.WithHTTPClient(&client), rp.WithAuthStyle(oauth2.AuthStyleInParams))
	if err != nil {
		return nil, cmp.Or(ctx.Err(), fmt.Errorf("OIDC discovery: %w", errOAuthFailed))
	}
	device, revoke := party.GetDeviceAuthorizationEndpoint(), party.GetRevokeEndpoint()
	if !httpsURL(party.OAuthConfig().Endpoint.TokenURL) || device != "" && !httpsURL(device) || revoke != "" && !httpsURL(revoke) {
		return nil, errMalformedResponse
	}
	return party, nil
}

func (m *Client) oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	party, err := m.relyingParty(ctx, m.info.OAuth.Issuer, m.info.OAuth.CLIClientID)
	if err != nil {
		return nil, err
	}
	config := party.OAuthConfig()
	config.Endpoint.DeviceAuthURL = party.GetDeviceAuthorizationEndpoint()
	return config, nil
}

type boundedTransport struct {
	base http.RoundTripper
}

func (t boundedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err := clientutil.ReadLimited(response.Body, maxDiscoveryBytes)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
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

func (m *Client) deviceToken(ctx context.Context, audience string) (*oauth2.Token, error) {
	config, err := m.oauthConfig(ctx)
	if err != nil {
		return nil, err
	}
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
	if !httpsURL(device.VerificationURI) || !validUserCode || !validToken(device.DeviceCode) ||
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
	if m.machine != nil {
		return m.machineToken(ctx, audience)
	}
	var result *oauth2.Token
	var initial bool
	err := m.withLock(ctx, func() (err error) {
		initial = m.state.Version == ""
		result, err = m.token(ctx, audience)
		return err
	})
	switch {
	case signedOut(err) && initial && m.interactive:
		if err := m.loginInteractive(ctx); err != nil {
			return nil, err
		}
		return m.resourceToken(ctx, audience)
	case signedOut(err):
		return nil, fmt.Errorf("%w; run holon login %s", err, m.client.baseURL)
	case errors.Is(err, errNoAudienceToken) && m.interactive:
		token, err := m.deviceToken(ctx, audience)
		if err != nil {
			return nil, err
		}
		return token, m.withLock(ctx, func() error {
			if err := m.loadCredentials(); err != nil {
				return err
			}
			m.state.Tokens[audience] = token
			return m.writeState(m.state)
		})
	}
	return result, err
}

func (m *Client) machineToken(ctx context.Context, audience string) (*oauth2.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A response may omit expires_in per [RFC 6749 §5.1]. Its zero Expiry fails this check, so the token is renewed.
	//
	// [RFC 6749 §5.1]: https://www.rfc-editor.org/rfc/rfc6749.html#section-5.1
	if cached := m.state.Tokens[audience]; cached != nil && time.Until(cached.Expiry) > accessSafety {
		return cached, nil
	}
	config, err := m.oauthConfig(ctx)
	if err != nil {
		return nil, err
	}
	credentials := *m.machine
	credentials.TokenURL = config.Endpoint.TokenURL
	credentials.EndpointParams = url.Values{"resource": {audience}}
	token, err := credentials.Token(m.oauthContext(ctx))
	if err != nil {
		return nil, cmp.Or(ctx.Err(), errOAuthFailed)
	}
	if !validOAuthToken(token) {
		return nil, errMalformedResponse
	}
	m.state.Tokens[audience] = token
	return token, nil
}

func (m *Client) Call(ctx context.Context, plane protocol.Plane, tool string, input, output any) error {
	token, err := m.controlToken(ctx)
	if err != nil {
		return err
	}
	return m.client.Call(ctx, plane, token, tool, input, output)
}
