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

// replaceExecution upserts desired's apps into session name and removes the apps named in remove.
func replaceExecution(ctx context.Context, manager *Client, name string,
	baseline protocol.SessionResponse, desired protocol.AppsRequestFor[protocol.App], remove ...string,
) (protocol.SessionResponse, error) {
	current := baseline
	desired.RequestID = cmp.Or(desired.RequestID, strings.ToLower(rand.Text()))
	for range 3 {
		token, err := manager.controlToken(ctx)
		if err != nil {
			return protocol.SessionResponse{}, err
		}
		input := protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]{
			SessionRequest:    protocol.SessionRequest{Session: name},
			WritePrecondition: protocol.WritePrecondition{ExpectedRevision: current.Revision, CreateOnly: current.Revision == ""}, Desired: desired,
			Remove: remove,
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
		if len(protocol.ChangedApps(namedApps(latest.Apps, desired.Apps), desired.Apps)) == 0 &&
			!slices.ContainsFunc(latest.Apps, func(app protocol.App) bool { return slices.Contains(remove, app.Name) }) {
			return retryOperation(ctx, manager, latest)
		}
		if !precondition {
			return protocol.SessionResponse{}, errors.New("replacement outcome conflicts with current desired state")
		}
		if baseline.Revision == "" {
			return protocol.SessionResponse{}, errors.New("execution was created concurrently")
		}
		if concurrent := protocol.ChangedApps(namedApps(baseline.Apps, desired.Apps), namedApps(latest.Apps, desired.Apps)); len(concurrent) != 0 {
			return protocol.SessionResponse{}, errors.New("application " + concurrent[0] + " changed concurrently")
		}
		current = latest
	}
	return protocol.SessionResponse{}, errors.New("execution kept changing after three write attempts")
}

func namedApps(apps, named []protocol.App) []protocol.App {
	return slices.DeleteFunc(slices.Clone(apps), func(app protocol.App) bool {
		return !slices.ContainsFunc(named, func(candidate protocol.App) bool { return candidate.Name == app.Name })
	})
}
