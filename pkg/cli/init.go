package cli

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/protocol"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/holon-sh/cli/pkg/scaffold"
	"github.com/holon-sh/cli/pkg/typegen"
)

// NewInitCommand builds holon init, which creates an app.
func NewInitCommand() *cobra.Command {
	var language, name string
	cmd := &cobra.Command{
		Use:   "init [DIR]",
		Short: "Create an app: an MCP server and the view it serves",
		Long: `Create an app in DIR, or the working directory: an MCP server in Go, Python or TypeScript, and a
view it serves as an MCP App and as a page.

DIR must be empty or absent; its name is the app's name. holon init pins the Holon SDKs that read the
clients this holon writes, installs the dependencies, generates the clients, and adds the app to the
working directory's holon.yaml when DIR is inside it, or else writes one in DIR.`,
		Example: `  # A Go app in ./orders
  holon init orders --lang go

  # A Python app in ./orders
  holon init orders --lang py

  # A TypeScript app in the working directory, with its MCP server name
  holon init --lang ts --name io.github.acme/orders`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, cmp.Or(cmd.Flags().Arg(0), "."), language, name)
		},
	}
	cmd.Flags().StringVar(&language, "lang", "", "The server's language: go, py or ts")
	cmd.Flags().StringVar(&name, "name", "", "The MCP server name (default com.example/APP)")
	return cmd
}

func runInit(cmd *cobra.Command, dirArg, language, name string) error {
	dir, err := filepath.Abs(dirArg)
	if err != nil {
		return err
	}
	app := filepath.Base(dir)
	if !protocol.ValidAppName(app) {
		return fmt.Errorf("%s cannot name an app: name the directory with a lowercase DNS label, such as orders", app)
	}
	if err := scaffold.Empty(dirArg); err != nil {
		return err
	}
	ask := prompter(cmd)
	if language == "" {
		if ask == nil {
			return errors.New("--lang is required: go, py or ts")
		}
		if language, err = ask("Language (go, py, ts)", ""); err != nil {
			return err
		}
	}
	if !slices.Contains(scaffold.Languages, language) {
		return fmt.Errorf("--lang %q: use go, py or ts", language)
	}
	if name == "" {
		name = "com.example/" + app
		if ask != nil {
			if name, err = ask("MCP server name", name); err != nil {
				return err
			}
		}
	}
	if !typegen.ValidServerName(name) || typegen.ServerKey(name) == "" {
		return fmt.Errorf("%s is not an MCP server name such as io.github.acme/%s", name, app)
	}
	manifest, appPath, err := manifestFor(dir)
	if err != nil {
		return err
	}

	versions, err := scaffold.Resolve(cmd.Context(), language, scaffold.DefaultRegistries())
	if err != nil {
		return err
	}
	stderr := cmd.ErrOrStderr()
	files, err := scaffold.Project{Dir: dir, Language: language, Name: name, Versions: versions}.Write()
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "%s: wrote %s\n", app, strings.Join(files, ", "))
	if err := scaffold.Register(manifest, app, appPath); err != nil {
		return err
	}
	if working, err := os.Getwd(); err == nil {
		if relative, err := filepath.Rel(working, manifest); err == nil {
			manifest = relative
		}
	}
	fmt.Fprintf(stderr, "%s: added to %s\n", app, manifest)

	// Python and Go dependencies first, so that generating finds the server's packages and the Go
	// runtime; generating installs the Node.js ones itself.
	switch language {
	case "py":
		err = install(cmd, dir, dirArg, "https://docs.astral.sh/uv/", "uv", "sync")
	case "go":
		err = install(cmd, dir, dirArg, "https://go.dev/dl/", "go", "get", "github.com/holon-sh/sdk-go@latest", "github.com/modelcontextprotocol/go-sdk@latest")
	}
	if err != nil {
		return err
	}
	c, err := consumer(dir, nil)
	if err == nil {
		err = typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: io.Discard, Stderr: stderr})
	}
	if err != nil {
		return fmt.Errorf("%w\n%s is written; run holon types generate in it once that is fixed", err, dirArg)
	}
	// The generated clients' imports complete go.sum.
	if language == "go" {
		if err := install(cmd, dir, dirArg, "https://go.dev/dl/", "go", "mod", "tidy"); err != nil {
			return err
		}
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Next:")
	if appPath == "." {
		if dirArg != "." {
			fmt.Fprintf(out, "  cd %s\n", dirArg)
		}
		fmt.Fprintln(out, "  holon dev")
	} else {
		fmt.Fprintf(out, "  holon dev %s\n", app)
	}
	return nil
}

// install runs a package manager's command in dir, the app's directory, which the user named dirArg.
func install(cmd *cobra.Command, dir, dirArg, download string, args ...string) error {
	command := strings.Join(args, " ")
	if _, err := exec.LookPath(args[0]); err != nil {
		return fmt.Errorf("%s is not installed (%s); install it, then run %s and holon types generate in %s", args[0], download, command, dirArg)
	}
	stderr := cmd.ErrOrStderr()
	fmt.Fprintf(stderr, "%s: %s\n", filepath.Base(dir), command)
	run := exec.CommandContext(cmd.Context(), args[0], args[1:]...)
	run.Dir, run.Stdout, run.Stderr = dir, stderr, stderr
	if err := run.Run(); err != nil {
		return fmt.Errorf("%s in %s: %w", command, dirArg, err)
	}
	return nil
}

// manifestFor is the holon.yaml an app in dir joins, and the app's path in it: the working
// directory's when dir is inside it, or else a new one in dir.
func manifestFor(dir string) (manifest, path string, err error) {
	working, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	if relative, err := filepath.Rel(working, dir); err == nil && relative != "." && filepath.IsLocal(relative) {
		if file := projectFile(working); file != "" {
			return file, filepath.ToSlash(relative), nil
		}
	}
	return filepath.Join(dir, "holon.yaml"), ".", nil
}

// prompter asks for a value on the terminal, returning fallback for an empty answer. It is nil when
// standard input is not a terminal.
func prompter(cmd *cobra.Command) func(question, fallback string) (string, error) {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return nil
	}
	reader := bufio.NewReader(in)
	return func(question, fallback string) (string, error) {
		if fallback != "" {
			question += " (" + fallback + ")"
		}
		fmt.Fprint(cmd.ErrOrStderr(), question+": ")
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return cmp.Or(strings.TrimSpace(line), fallback), nil
	}
}
