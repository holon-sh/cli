package commandenv

import (
	"context"
	"fmt"
	"io"
	"strings"
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
