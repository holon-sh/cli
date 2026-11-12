package commandenv

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

func Run(ctx context.Context, executable string, args []string, stdin []byte, env []string) (string, error) {
	var stdout, stderr strings.Builder
	err := RunStreaming(ctx, "", executable, args, bytes.NewReader(stdin), env, &stdout, &stderr)
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
		return "", fmt.Errorf("%s: %w: %s", executable, err, message)
	}
	return "", fmt.Errorf("%s: %w", executable, err)
}
