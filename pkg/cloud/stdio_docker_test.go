//go:build docker

package cloud

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/cli/internal/commandenv"
)

func dockerTest(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	var output strings.Builder
	if err := commandenv.RunStreaming(ctx, "docker", args, nil, nil, &output, os.Stderr); err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, output.String())
	}
	return strings.TrimSpace(output.String())
}
