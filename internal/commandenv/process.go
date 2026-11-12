package commandenv

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

func Run(ctx context.Context, executable string, args, env []string) (string, error) {
	var stdout, stderr strings.Builder
	err := RunStreaming(ctx, "", executable, args, nil, env, &stdout, &stderr)
	if err == nil || ctx.Err() != nil {
		return stdout.String(), err
	}
	message := strings.TrimSpace(stderr.String())
	if len(message) > 4096 {
		end := 4096
		for end > 0 && !utf8.RuneStart(message[end]) {
			end--
		}
		message = message[:end] + "..."
	}
	if message != "" {
		return "", fmt.Errorf("%w: %s", err, message)
	}
	return "", err
}
