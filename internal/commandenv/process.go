package commandenv

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/holon-sh/cli/internal/clientutil"
)

func Run(ctx context.Context, executable string, args []string, stdin []byte, env []string) (string, error) {
	var stdout, stderr strings.Builder
	err := RunStreaming(ctx, executable, args, bytes.NewReader(stdin), env, &stdout, &stderr)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.String(), ctxErr
		}
		message := strings.TrimSpace(stderr.String())
		if len(message) > 4096 {
			message = clientutil.HeadUTF8(message, 4096) + "..."
		}
		if message != "" {
			return "", fmt.Errorf("%s: %w: %s", executable, err, message)
		}
		return "", fmt.Errorf("%s: %w", executable, err)
	}
	return stdout.String(), nil
}
