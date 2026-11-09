package cli

import (
	"bufio"
	"bytes"
	"cmp"
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

	"github.com/holon-sh/cli/internal/commandenv"
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
view it serves as an MCP App and as a page. The app is named after DIR.

In an empty or absent DIR, holon init writes a new project. In a Go module (go.mod), a Python
project (pyproject.toml) or a Node.js project (package.json), it adds the server and the view beside
the project's own files: Go and Python dependencies through go get and uv, npm packages into
package.json, and lines to .gitignore. It leaves the project's scripts and tsconfig.json as written.

holon init pins the newest Holon SDK releases, on npm those that read the clients this holon writes,
installs the dependencies, generates the clients, and adds the app to the working directory's
holon.yaml when DIR is inside it, or else to DIR's.`,
		Example: `  # A Go app in ./orders
  holon init orders --lang go

  # A Python app in ./orders
  holon init orders --lang py

  # A TypeScript app in the working directory, with its MCP server name
  holon init --lang ts --name io.github.acme/orders

  # Add an app to the Python project in the working directory
  holon init`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, cmp.Or(cmd.Flags().Arg(0), "."), language, name)
		},
	}
	cmd.Flags().StringVar(&language, "lang", "", "The server's language: go, py or ts; detected in an existing project")
	cmd.Flags().StringVar(&name, "name", "", "The MCP server name (default com.example/APP)")
	return cmd
}

func runInit(cmd *cobra.Command, dirArg, language, name string) error {
	dir, err := filepath.Abs(dirArg)
	if err != nil {
		return err
	}
	app := scaffold.AppName(dir)
	if !protocol.ValidAppName(app) {
		return fmt.Errorf("%s cannot name an app: name the directory with a lowercase DNS label, such as orders", filepath.Base(dir))
	}
	languages, existing, err := scaffold.Detect(dir)
	if err != nil {
		return err
	}
	ask := prompter(cmd)
	switch {
	case language != "":
	case existing && len(languages) == 0:
		return fmt.Errorf("%s has no go.mod, pyproject.toml or package.json: holon init adds an app to a Go, Python or Node.js project, or creates one in an empty directory", dirArg)
	case existing && len(languages) == 1:
		language = languages[0]
	case ask == nil:
		return fmt.Errorf("--lang is required: %s", strings.Join(languages, " or "))
	default:
		if language, err = ask("Language ("+strings.Join(languages, ", ")+")", ""); err != nil {
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
	manager, err := typegen.PackageManager(dir)
	if err != nil {
		return err
	}
	for _, tool := range []string{tools[language], manager} {
		if _, err := exec.LookPath(tool); tool != "" && err != nil {
			return fmt.Errorf("a %s app needs %s; install it and run holon init again", language, tool)
		}
	}
	project := scaffold.Project{Dir: dir, Language: language, Name: name}
	if err := project.Check(); err != nil {
		return err
	}
	manifestPath, appPath, err := manifestFor(dir)
	if err != nil {
		return err
	}
	data, err := addApp(manifestPath, app, appPath)
	if err != nil {
		return err
	}

	if project.Versions, err = scaffold.Resolve(cmd.Context(), language); err != nil {
		return err
	}
	stderr := cmd.ErrOrStderr()
	created, changed, notes, err := project.Write()
	if err != nil {
		return err
	}
	if len(created) > 0 {
		fmt.Fprintf(stderr, "%s: wrote %s\n", app, strings.Join(created, ", "))
	}
	if len(changed) > 0 {
		fmt.Fprintf(stderr, "%s: updated %s\n", app, strings.Join(changed, ", "))
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "%s: added to %s\n", app, scaffold.Relative(manifestPath))

	run := func(args ...string) error {
		fmt.Fprintf(stderr, "%s: %s\n", app, strings.Join(args, " "))
		if err := commandenv.RunStreaming(cmd.Context(), dir, args[0], args[1:], nil, nil, stderr, stderr); err != nil {
			return fmt.Errorf("%s in %s: %w", strings.Join(args, " "), dirArg, err)
		}
		return nil
	}
	install := func() error {
		var commands [][]string
		switch language {
		case typegen.TargetGo:
			commands = append(commands, []string{"go", "get", typegen.GoRuntimeModule + "@latest"})
		case typegen.TargetPy:
			var installs bytes.Buffer
			if err := commandenv.RunStreaming(cmd.Context(), dir, "uv", []string{"export", "--no-hashes", "--quiet"}, nil, nil, &installs, stderr); err != nil {
				return fmt.Errorf("uv export in %s: %w", dirArg, err)
			}
			installed := map[string]bool{}
			for line := range strings.Lines(installs.String()) {
				installed[typegen.PythonName(line)] = true
			}
			dependencies, dev := project.PythonRequirements()
			commands = append(commands, append([]string{"uv", "add", "--frozen"}, dependencies...))
			if dev = slices.DeleteFunc(dev, func(r string) bool { return installed[typegen.PythonName(r)] }); len(dev) > 0 {
				commands = append(commands, append([]string{"uv", "add", "--frozen", "--dev"}, dev...))
			}
			commands = append(commands, []string{"uv", "sync"})
		}
		for _, command := range append(commands, []string{manager, "install"}) {
			if err := run(command...); err != nil {
				return err
			}
		}
		c, err := consumer(dir, nil)
		if err != nil {
			return err
		}
		return typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: io.Discard, Stderr: stderr})
	}
	if err := install(); err != nil {
		return fmt.Errorf("%w\nthe app is written; run holon types generate in %s once that is fixed", err, dirArg)
	}
	if language == typegen.TargetGo {
		for _, command := range [][]string{{manager, "run", "build"}, {"go", "mod", "tidy"}} {
			if err := run(command...); err != nil {
				return err
			}
		}
	}

	for _, note := range notes {
		fmt.Fprintf(stderr, "%s: %s\n", app, note)
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

// tools are the toolchains an app in a language needs besides its Node.js package manager.
var tools = map[string]string{typegen.TargetGo: "go", typegen.TargetPy: "uv"}

// manifestFor is the holon.yaml an app in dir joins, and the app's path in it: the working
// directory's when dir is inside it, or else dir's.
func manifestFor(dir string) (manifest, path string, err error) {
	working, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	if relative, err := filepath.Rel(working, dir); err == nil && filepath.IsLocal(relative) {
		if file := projectFile(working); file != "" {
			return file, filepath.ToSlash(relative), nil
		}
	}
	if file := projectFile(dir); file != "" {
		return file, ".", nil
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
