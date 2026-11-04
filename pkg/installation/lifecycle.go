package installation

import (
	"context"
	"fmt"
	"slices"
	"time"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/protocol"
)

func lifecyclePending(s protocol.SessionResponse) bool {
	lifecycle := ptr.Deref(s.Lifecycle, protocol.LifecycleStatus{})
	return s.Operation != nil && (lifecycle.ID != s.Operation.ID || lifecycle.Phase != "Succeeded")
}

func lifecycleFailed(s protocol.SessionResponse) bool {
	lifecycle := ptr.Deref(s.Lifecycle, protocol.LifecycleStatus{})
	return s.Operation != nil && lifecycle.Phase == "Failed" && lifecycle.Retry == s.Operation.Retry
}

func (m *Client) authorizeOperation(ctx context.Context, s protocol.SessionResponse) error {
	lifecycle := ptr.Deref(s.Lifecycle, protocol.LifecycleStatus{})
	if !lifecyclePending(s) ||
		m.info.OAuth == nil ||
		lifecycle.ID != s.Operation.ID ||
		lifecycle.Hook == "" ||
		lifecycle.Phase == "Failed" {
		return nil
	}
	key := s.UID + "/" + s.Operation.ID
	if m.hookSent[key].After(m.now()) {
		return nil
	}
	mode := protocol.ModeDevelopment
	if s.Name == protocol.ProductionName {
		mode = protocol.ModeProduction
	}
	audience, err := protocol.SessionAudience(m.client.baseURL, mode, s.Name)
	if err != nil {
		return err
	}
	apps := slices.Clone(s.Apps)
	for _, release := range s.Releases {
		if release.Installed != nil {
			apps = append(apps, *release.Installed)
		}
	}
	scopes := protocol.SessionScopes(mode, apps)
	token, err := m.resourceToken(ctx, audience, scopes)
	if err != nil {
		return fmt.Errorf("authorize lifecycle: %w", err)
	}

	err = m.Call(ctx, ControlPlane(s.Name), protocol.ToolAuthorizeLifecycle, protocol.AuthorizeLifecycleRequest{
		SessionRequest: protocol.SessionRequest{Session: s.Name}, Operation: s.Operation.ID, AccessToken: token.AccessToken,
	}, nil)
	if err == nil {
		now := m.now()
		m.hookSent[key] = now.Add(min(time.Minute, token.Expiry.Add(-accessSafety).Sub(now)))
	}
	return err
}

func retryOperation(ctx context.Context, manager *Client, s protocol.SessionResponse) (protocol.SessionResponse, error) {
	if !lifecycleFailed(s) {
		return s, nil
	}
	var result protocol.SessionResponse
	err := manager.Call(ctx, ControlPlane(s.Name), protocol.ToolRetryLifecycle, protocol.RetryLifecycleRequest{
		SessionRequest: protocol.SessionRequest{Session: s.Name}, Operation: s.Operation.ID, ExpectedRevision: s.Revision,
	}, &result)
	return result, err
}
