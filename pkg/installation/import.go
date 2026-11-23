package installation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/holon-sh/protocol"
)

func importRegistryBuild(
	ctx context.Context, manager *Client, host protocol.Host, app manifestApp,
) (protocol.ResolvedAppBuild, error) {
	var build protocol.ResolvedAppBuild
	var imported protocol.Import
	err := manager.Call(ctx, host, protocol.ToolImportRegistryPackage, protocol.ImportRegistryPackageRequest{App: app.Name, Registry: *app.resolved.pkg}, &imported)
	if err == nil {
		err = wait.PollUntilContextCancel(ctx, workflowPoll, true, func(ctx context.Context) (bool, error) {
			if err := manager.Call(ctx, host, protocol.ToolGetImport, protocol.GetImportRequest{ID: imported.ID}, &imported); err != nil {
				return false, err
			}
			if imported.Phase == protocol.PhaseFailed {
				return false, errors.New("import failed: " + imported.Message)
			}
			return imported.Build != nil, nil
		})
	}
	if err != nil {
		return build, fmt.Errorf("import %s: %w", app.Name, err)
	}
	if err := manager.Call(ctx, host, protocol.ToolResolveAppBuild, *imported.Build, &build); err != nil {
		return build, err
	}
	if len(build.UnsupportedReasons) != 0 {
		return build, errors.New(strings.Join(build.UnsupportedReasons, "; "))
	}
	return build, nil
}
