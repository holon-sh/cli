// Package cli provides the portable Holon commands for standalone and extended executables.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/spf13/cobra"
)

func examples(command string) string {
	return `  # A server directory
  ` + command + ` orders

  # A server entry
  ` + command + ` orders/server.py

  # An MCP registry server
  ` + command + ` https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4

  # A remote MCP server, with the client named docs
  ` + command + ` docs=https://mcp.context7.com/mcp`
}

var alias = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?=`)
var registryPath = regexp.MustCompile(`^/v0(?:\.1)?/servers/([^/]+)/versions/([^/]+)$`)

// parseSource reads one SERVER argument. Paths are resolved against the working directory.
func parseSource(arg string) (typegen.Source, error) {
	var source typegen.Source
	if match := alias.FindString(arg); match != "" {
		source.Alias, arg = strings.TrimSuffix(match, "="), arg[len(match):]
	}
	if arg == "" {
		return source, errors.New("a server is required after ALIAS=")
	}
	if u, err := url.Parse(arg); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if m := registryPath.FindStringSubmatch(u.EscapedPath()); m != nil {
			// EscapedPath guarantees valid escapes in each slash-delimited segment.
			name, _ := url.PathUnescape(m[1])
			version, _ := url.PathUnescape(m[2])
			source.Registry = &typegen.Reference{Name: name, Version: version, Registry: u.Scheme + "://" + u.Host}
			return source, nil
		}
		source.Endpoint = arg
		return source, nil
	}
	path, err := filepath.Abs(arg)
	if err != nil {
		return source, err
	}
	source.Path = path
	return source, nil
}

// consumer builds the working directory's consumer. Its targets are the runtimes the project declares;
// a server inside the directory supplies the project's own identity and contract.
func consumer(args []string) (typegen.Consumer, error) {
	dir, err := os.Getwd()
	if err != nil {
		return typegen.Consumer{}, err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return typegen.Consumer{}, err
	}
	targets, err := typegen.DeclaredTargets(dir)
	if err != nil {
		return typegen.Consumer{}, err
	}
	c := typegen.Consumer{Name: filepath.Base(dir), Dir: dir, Targets: targets}
	for _, arg := range args {
		source, err := parseSource(arg)
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
	if len(args) == 0 {
		return c, errors.New("at least one SERVER is required")
	}
	return c, nil
}

func insideDir(path, dir string) bool {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	relative, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(relative)
}

// printContract prints one server's contract document and writes no files.
func printContract(cmd *cobra.Command, arg string) error {
	source, err := parseSource(arg)
	if err != nil {
		return fmt.Errorf("%s: %w", arg, err)
	}
	metadata, err := typegen.Resolve(cmd.Context(), source, typegen.Options{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return err
	}
	document := struct {
		Version  int                   `json:"version"`
		Identity mcpcontract.Identity  `json:"identity"`
		Contract *mcpcontract.Contract `json:"contract"`
	}{mcpcontract.Version, metadata.Identity, metadata.Contract}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

// newCheck builds the check command; it is registered as "holon types check" and as the
// "holon check" shortcut (a cobra command has one parent, so each gets its own instance).
func newCheck(short string) *cobra.Command {
	return &cobra.Command{Use: "check", Short: short, Long: "Type-check the project's calls to MCP servers.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		if dir, err = filepath.EvalSymlinks(dir); err != nil {
			return err
		}
		return typegen.Check(cmd.Context(), dir, typegen.Options{Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
	}}
}

// NewCommand builds the standalone CLI, including the check shortcut.
func NewCommand(version string) *cobra.Command {
	root := &cobra.Command{Use: "holon", Short: "Generate and check typed MCP clients", Version: version + " (standalone types CLI)", SilenceUsage: true, SilenceErrors: true}
	root.SetVersionTemplate("holon {{.Version}}\n")
	root.AddCommand(NewTypesCommand(), newCheck("Shortcut for holon types check"))
	return root
}

// NewTypesCommand builds an independent types command tree that can be added to a host CLI.
// It has no installation, project, or control-plane dependencies.
func NewTypesCommand() *cobra.Command {
	// NoArgs makes removed subcommands such as "types contract" fail instead of printing help.
	types := &cobra.Command{Use: "types", Short: "Generate and check typed MCP clients and print MCP contracts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var verbose, contract bool
	generate := &cobra.Command{Use: "generate SERVER...", Short: "Generate typed clients for MCP servers", Long: "Generate typed clients for MCP servers.", Example: examples("holon types generate"), RunE: func(cmd *cobra.Command, args []string) error {
		if contract {
			if len(args) != 1 {
				return errors.New("--contract takes exactly one SERVER")
			}
			return printContract(cmd, args[0])
		}
		c, err := consumer(args)
		if err != nil {
			return err
		}
		progress := io.Discard
		if verbose {
			progress = cmd.ErrOrStderr()
		}
		return typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: progress, Stderr: cmd.ErrOrStderr()})
	}}
	generate.Flags().BoolVar(&contract, "contract", false, "Print one server's MCP contract instead of generating")
	generate.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show generation details")
	types.AddCommand(generate)
	types.AddCommand(newCheck("Type-check the project's calls to MCP servers"))

	return types
}
