package installation

import (
	"fmt"
	"os"

	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol/hookartifact"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/internal/commandenv"
)

func snapshotSource(app manifestApp, destination string) (string, error) {
	tlsDir, err := commandenv.TLSDirectory()
	if err != nil {
		return "", err
	}
	if clientutil.PathInside(tlsDir, app.Path) {
		return "", fmt.Errorf("build context for %s must not contain Holon's certificate authority directory", app.Name)
	}
	return typegen.SnapshotSource(app.Name, app.Path, destination)
}

func sourceAppStart(app manifestApp, root string) ([]string, error) {
	if _, err := hookartifact.Discover(os.DirFS(root)); err != nil {
		return nil, err
	}
	start, err := typegen.SourceStart(root)
	if err != nil {
		return nil, err
	}
	if !app.resolved.stdio && start != nil {
		return start, nil
	}
	return nil, fmt.Errorf("%s: holon dev and holon deploy run an app from a server.py, server.ts or server.js entry; ship it as an image: app", app.Name)
}
