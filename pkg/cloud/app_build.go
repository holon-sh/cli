package cloud

import (
	"cmp"
	"context"
	"net/http"
	"time"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/protocol"
)

func publishAppBuild(ctx context.Context, app manifestApp, image string, grant protocol.UploadGrant, options buildOptions) error {
	record := mcpregistry.BuildRecord(app.resolved.App, app.resolved.catalogEnv, app.resolved.descriptor)
	client := cmp.Or(options.HTTPClient, &http.Client{Timeout: 5 * time.Minute})
	repository, err := imageRepository(grant.Registry+"/"+grant.Repositories[app.Name], grant.Grant, client)
	if err != nil {
		return err
	}
	executable, err := repository.Resolve(ctx, image)
	if err != nil {
		return err
	}
	return appartifact.Publish(ctx, repository, executable, record, options.catalogTag)
}
