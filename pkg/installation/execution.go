package installation

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/holon-sh/protocol"
)

func desiredFor(session protocol.SessionResponse) protocol.AppsRequestFor[protocol.App] {
	return protocol.AppsRequestFor[protocol.App]{Apps: protocol.WritableApps(session.Apps)}
}

func replaceExecution(ctx context.Context, manager *Client, name string,
	baseline protocol.SessionResponse, desired protocol.AppsRequestFor[protocol.App], targeted bool) (protocol.SessionResponse,
	error,
) {
	current := baseline
	target := desired
	target.RequestID = cmp.Or(target.RequestID, strings.ToLower(rand.Text()))
	for range 3 {
		token, err := manager.controlToken(ctx)
		if err != nil {
			return protocol.SessionResponse{}, err
		}
		input := protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]{
			SessionRequest:    protocol.SessionRequest{Session: name},
			WritePrecondition: protocol.WritePrecondition{ExpectedRevision: current.Revision, CreateOnly: current.Revision == ""}, Desired: target,
		}
		var accepted protocol.SessionResponse
		err = manager.client.Call(ctx, ControlPlane(name), token, protocol.ToolUpdateSession, input, &accepted)
		if err == nil {
			return retryOperation(ctx, manager, accepted)
		}
		status := HTTPStatus(err)
		precondition := status == http.StatusPreconditionFailed
		if status != 0 && !precondition && status < 500 {
			return protocol.SessionResponse{}, err
		}
		latest, readErr := manager.Session(ctx, name)
		if readErr != nil {
			return protocol.SessionResponse{}, errors.Join(err, readErr)
		}
		if current.UID != "" && latest.UID != current.UID {
			return protocol.SessionResponse{}, errors.New("execution identity changed")
		}
		if len(protocol.ChangedApps(latest.Apps, target.Apps)) == 0 {
			return retryOperation(ctx, manager, latest)
		}
		if !precondition {
			return protocol.SessionResponse{}, errors.New("replacement outcome conflicts with current desired state")
		}
		if baseline.Revision == "" {
			return protocol.SessionResponse{}, errors.New("execution was created concurrently")
		}
		concurrent := protocol.ChangedApps(baseline.Apps, latest.Apps)
		if !targeted && len(concurrent) != 0 {
			return protocol.SessionResponse{}, errors.New("execution changed concurrently")
		}
		merged := desiredFor(latest)
		for _, appName := range protocol.ChangedApps(baseline.Apps, desired.Apps) {
			if slices.Contains(concurrent, appName) {
				return protocol.SessionResponse{}, errors.New("application " + appName + " changed concurrently")
			}
			merged.Apps = slices.DeleteFunc(merged.Apps, func(app protocol.App) bool { return app.Name == appName })
			if index := slices.IndexFunc(desired.Apps, func(app protocol.App) bool { return app.Name == appName }); index >= 0 {
				merged.Apps = append(merged.Apps, desired.Apps[index])
			}
		}
		current, target = latest, merged
	}
	return protocol.SessionResponse{}, errors.New("execution kept changing after three write attempts")
}
