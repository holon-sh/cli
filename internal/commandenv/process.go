package commandenv

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// RunEach runs commands in dir in order, naming each on stderr after label, and stops at the first that fails.
func RunEach(ctx context.Context, dir, label string, commands [][]string, env []string, stderr io.Writer) error {
	for _, command := range commands {
		fmt.Fprintf(stderr, "%s: %s\n", label, strings.Join(command, " "))
		if err := RunStreaming(ctx, dir, command[0], command[1:], nil, env, stderr, stderr); err != nil {
			return fmt.Errorf("%s in %s: %w", strings.Join(command, " "), dir, err)
		}
	}
	return nil
}

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
