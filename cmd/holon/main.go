package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/holon-sh/cli/pkg/cli"
	"github.com/holon-sh/cli/pkg/installation"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := cli.NewCommand(version, installation.ClientConfig{Output: os.Stderr}.AppToken)
	command.AddCommand(installation.Commands()...)
	command.SetIn(os.Stdin)
	command.SetOut(os.Stdout)
	command.SetErr(os.Stderr)
	err := command.ExecuteContext(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "holon: %s\n", err)
		return 1
	}
	return 0
}
