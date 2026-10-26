// holon-tooling is the compatibility host for the embedded language runners.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/holon-sh/cli/pkg/tooling/core"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 6 {
		fmt.Fprintln(os.Stderr, "usage: holon-tooling LANGUAGE INTERPRETER RUNNER MODE CONFIG [OUTPUT]")
		os.Exit(1)
	}
	r := core.Runner{Language: os.Args[1], Program: os.Args[2], Path: os.Args[3], Stdout: os.Stdout, Stderr: os.Stderr}
	output := ""
	if len(os.Args) > 6 {
		output = os.Args[6]
	}
	if err := r.Run(ctx, os.Args[4], os.Args[5], output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var status *exec.ExitError
		if errors.As(err, &status) && status.ExitCode() > 0 {
			os.Exit(status.ExitCode())
		}
		os.Exit(1)
	}
}
