// Package cli provides the portable Holon commands for standalone and extended executables.
package cli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/spf13/cobra"
)

const dependencyHelp = "Leverages sh.holon.requires in the publisher-provided metadata field (https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) to resolve dependencies, or the -s list for explicit dependencies."

const generateExamples = `  # Generate for the current project
  holon types generate

  # Another project
  holon types generate ../checkout

  # Name the project's own server to call it from outside the browser too, as apps.orders
  holon types generate -s orders=./server

  # Explicit dependencies: a server directory, a server entry, an MCP registry server, a remote MCP server named docs
  holon types generate -s ../orders
  holon types generate -s ../orders/server.py
  holon types generate -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
  holon types generate -s docs=https://mcp.context7.com/mcp

  # A remote MCP server that requires a token: DOCS_MCP_TOKEN is sent to it as a bearer token
  DOCS_MCP_TOKEN=... holon types generate -s docs=https://mcp.example.com/mcp

  # Print one server's contract, or commit it beside its server.json
  holon types generate --contract ../orders
  holon types generate --contract ../orders -o ../orders/holon-contract.json`

func oneDir(_ *cobra.Command, args []string) error {
	if len(args) > 1 {
		return errors.New("one DIR at most; dependencies are -s values")
	}
	return nil
}

// clientDir resolves the optional client directory, defaulting to the working directory.
func clientDir(path string) (string, error) {
	path = cmp.Or(path, ".")
	dir, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return filepath.EvalSymlinks(dir)
}

// consumer selects dependencies from -s, or what the project declares without it. A -s path inside
// dir is the project's own server, which supplies its identity and contract.
func consumer(dir string, servers []string) (typegen.Consumer, error) {
	targets, err := typegen.DeclaredTargets(dir)
	if err != nil {
		return typegen.Consumer{}, err
	}
	c := typegen.Consumer{Name: filepath.Base(dir), Dir: dir, Targets: targets}
	for _, arg := range servers {
		source, err := typegen.ParseSource(arg)
		if err != nil {
			return c, fmt.Errorf("%s: %w", arg, err)
		}
		if source.Path != "" && insideDir(source.Path, dir) {
			if c.Server != nil {
				return c, fmt.Errorf("%s: a project has one own server", arg)
			}
			c.Server = &source
			continue
		}
		c.Sources = append(c.Sources, source)
	}
	if len(servers) == 0 {
		if c.Server, c.Sources, err = declared(dir, targets); err != nil {
			return c, err
		}
	}
	if len(c.Sources) == 0 && c.Server == nil {
		return c, fmt.Errorf("%s declares no requires; pass -s", dir)
	}
	return c, nil
}

// declared returns the requires dir declares and, for a project with a browser slot, its own server
// and the requires declared beside that server.
func declared(dir string, targets []string) (*typegen.Source, []typegen.Source, error) {
	sources, err := typegen.DeclaredSources(dir)
	if err != nil || !slices.Contains(targets, typegen.TargetUI) {
		return nil, sources, err
	}
	own, err := typegen.OwnServer(dir)
	if err != nil || own == nil || own.Path == dir {
		return own, sources, err
	}
	beside, err := typegen.DeclaredSources(own.Path)
	return own, append(sources, beside...), err
}

func insideDir(path, dir string) bool {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	relative, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(relative)
}

// printContract prints one server's contract document, or writes it to output when given.
func printContract(cmd *cobra.Command, arg, output string) error {
	source, err := typegen.ParseSource(arg)
	if err != nil {
		return err
	}
	document, err := typegen.ResolveDocument(cmd.Context(), source, typegen.Options{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return err
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document.Metadata); err != nil {
		return err
	}
	if output != "" {
		return os.WriteFile(output, data.Bytes(), 0o644)
	}
	_, err = cmd.OutOrStdout().Write(data.Bytes())
	return err
}

// generateProject resolves the client directory and generates its declared slots from -s
// or server.json requires. It returns the directory for type-checking.
func generateProject(cmd *cobra.Command, servers []string, progress io.Writer) (string, error) {
	typed := cmd.Flags().Arg(0)
	dir, err := clientDir(typed)
	if err != nil {
		return "", err
	}
	c, err := consumer(dir, servers)
	if err != nil {
		return "", err
	}
	return dir, typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: progress, Stderr: cmd.ErrOrStderr()})
}

// newCheck builds the check command; it is registered as "holon types check" and as the
// "holon check" shortcut (a cobra command has one parent, so each gets its own instance).
func newCheck(short string) *cobra.Command {
	var servers []string
	var noGenerate bool
	cmd := &cobra.Command{Use: "check [DIR]", Short: short, Long: "Generate typed clients for MCP servers, then type-check the project's calls to them.\n\n" + dependencyHelp, Example: `  # Generate, then type-check
  holon types check

  # Type-check the existing clients only (no network, no writes)
  holon types check --no-generate

  # Explicit dependencies, as for generate
  holon types check -s orders=../orders`, Args: oneDir, RunE: func(cmd *cobra.Command, _ []string) error {
		if noGenerate && len(servers) > 0 {
			return errors.New("--no-generate type-checks the existing clients; -s has no effect")
		}
		var dir string
		var err error
		if noGenerate {
			dir, err = clientDir(cmd.Flags().Arg(0))
		} else {
			dir, err = generateProject(cmd, servers, io.Discard)
		}
		if err != nil {
			return err
		}
		return typegen.Check(cmd.Context(), dir, typegen.Options{Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
	}}
	cmd.Flags().StringArrayVarP(&servers, "server", "s", nil, serverHelp)
	cmd.Flags().BoolVar(&noGenerate, "no-generate", false, "Type-check the existing clients only: no network, no install, no writes")
	return cmd
}

const serverHelp = "A dependency, [ALIAS=]PATH or URL, paths relative to the working directory"

// NewCommand builds the standalone CLI, including the check shortcut.
func NewCommand(version string) *cobra.Command {
	root := &cobra.Command{Use: "holon", Short: "MCP developer client", Version: version, SilenceUsage: true, SilenceErrors: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("holon {{.Version}}\n")
	root.AddCommand(NewTypesCommand(), newCheck("Shortcut for holon types check"))
	return root
}

// NewTypesCommand builds an independent types command tree that can be added to a host CLI.
// It has no installation, project, or control-plane dependencies.
func NewTypesCommand() *cobra.Command {
	// NoArgs makes removed subcommands such as "types contract" fail instead of printing help.
	types := &cobra.Command{Use: "types", Short: "Generate and check typed MCP clients and print MCP contracts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var verbose bool
	var contract, output string
	var servers []string
	generate := &cobra.Command{Use: "generate [DIR]", Short: "Generate typed clients for MCP servers", Long: "Generate typed clients for MCP servers.\n\n" + dependencyHelp, Example: generateExamples, Args: oneDir, RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("contract") {
			if len(args) > 0 || len(servers) > 0 {
				return errors.New("--contract takes one SERVER and nothing else")
			}
			return printContract(cmd, contract, output)
		}
		if output != "" {
			return errors.New("--output writes a --contract document")
		}
		progress := io.Discard
		if verbose {
			progress = cmd.ErrOrStderr()
		}
		_, err := generateProject(cmd, servers, progress)
		return err
	}}
	generate.Flags().StringVar(&contract, "contract", "", "Print one server's MCP contract instead of generating")
	generate.Flags().StringVarP(&output, "output", "o", "", "Write the --contract document to FILE, such as holon-contract.json")
	generate.Flags().StringArrayVarP(&servers, "server", "s", nil, serverHelp)
	generate.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show generation details")
	types.AddCommand(generate, newCheck("Type-check the project's calls to MCP servers"))

	return types
}
