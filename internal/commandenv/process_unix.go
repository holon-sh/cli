//go:build darwin || linux

package commandenv

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

func RunStreaming(ctx context.Context, executable string, args []string, stdin io.Reader, env []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Cancel = func() error {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%s: %w", executable, err)
	}
	return nil
}
