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
	"regexp"
	"strings"

	"github.com/holon-sh/protocol"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/holon-sh/cli/pkg/scaffold"
	"github.com/holon-sh/cli/pkg/typegen"
)

// serverName is the MCP Registry's server name pattern: a reverse-DNS namespace and a name.
var serverName = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)

// NewInitCommand builds holon init, which creates an app.
func NewInitCommand() *cobra.Command {
	var language, name string
	cmd := &cobra.Command{
		Use:   "init [DIR]",
		Short: "Create an app: an MCP server and the view it serves",
		Long: `Create an app in DIR, or the working directory: an MCP server in Python or TypeScript, and a view it
serves as an MCP App and as a page.

DIR must be empty or absent; its name is the app's name. holon init pins the Holon SDKs that read the
clients this holon writes, installs the dependencies, generates the clients, and adds the app to the
working directory's holon.yaml when DIR is inside it, or else writes one in DIR.`,
		Example: `  # A Python app in ./orders
  holon init orders --lang py

  # A TypeScript app in the working directory, with its MCP server name
  holon init --lang ts --name io.github.acme/orders`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, cmp.Or(cmd.Flags().Arg(0), "."), language, name)
		},
	}
	cmd.Flags().StringVar(&language, "lang", "", "The server's language: py or ts")
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
			return errors.New("--lang is required: py or ts")
		}
		if language, err = ask("Language (py, ts)", ""); err != nil {
			return err
		}
	}
	switch language {
	case "py", "ts":
	case "go":
		return errors.New("--lang go is not available yet: a Go server cannot serve its view until sdk-go and Kiln support a frontend")
	default:
		return fmt.Errorf("--lang %q: use py or ts", language)
	}
	if name == "" {
		name = "com.example/" + app
		if ask != nil {
			if name, err = ask("MCP server name", name); err != nil {
				return err
			}
		}
	}
	if !serverName.MatchString(name) || typegen.ServerKey(name) == "" {
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

	// Python dependencies first, so that generating finds the server's packages; generating installs
	// the Node.js ones itself.
	if language == "py" {
		if _, err := exec.LookPath("uv"); err != nil {
			return fmt.Errorf("uv is not installed (https://docs.astral.sh/uv/); install it, then run uv sync and holon types generate in %s", dirArg)
		}
		fmt.Fprintf(stderr, "%s: uv sync\n", app)
		sync := exec.CommandContext(cmd.Context(), "uv", "sync")
		sync.Dir, sync.Stdout, sync.Stderr = dir, stderr, stderr
		if err := sync.Run(); err != nil {
			return fmt.Errorf("uv sync in %s: %w", dirArg, err)
		}
	}
	c, err := consumer(dir, nil)
	if err == nil {
		err = typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: io.Discard, Stderr: stderr})
	}
	if err != nil {
		return fmt.Errorf("%w\n%s is written; run holon types generate in it once that is fixed", err, dirArg)
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

// manifestFor is the holon.yaml an app in dir joins, and the app's path in it: the working
// directory's when dir is inside it, or else a new one in dir.
func manifestFor(dir string) (manifest, path string, err error) {
	working, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	if relative, err := filepath.Rel(working, dir); err == nil && relative != "." && filepath.IsLocal(relative) {
		for _, name := range projectFiles {
			if _, err := os.Lstat(filepath.Join(working, name)); err == nil {
				return filepath.Join(working, name), filepath.ToSlash(relative), nil
			}
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
