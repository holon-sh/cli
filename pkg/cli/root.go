// Package cli provides the portable Holon commands for standalone and extended executables.
package cli

import (
	"cmp"
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

const dependencyHelp = "Leverages sh.holon.requires in the publisher-provided metadata field (https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/official-registry-requirements.md#publisher-provided-metadata) from server.json (https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/generic-server-json.md) to resolve dependencies, or the -s list for explicit dependencies."

const generateExamples = `  # Generate for the current project, from server.json requires
  holon types generate

  # Another project
  holon types generate ../checkout

  # Explicit dependencies: a server directory, a server entry, an MCP registry server, a remote MCP server named docs
  holon types generate -s ../orders
  holon types generate -s ../orders/server.py
  holon types generate -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
  holon types generate -s docs=https://mcp.context7.com/mcp

  # Print one server's contract
  holon types generate --contract ../orders`

var alias = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?=`)
var registryPath = regexp.MustCompile(`^/v0(?:\.1)?/servers/([^/]+)/versions/([^/]+)$`)

// parseSource reads one SERVER argument. Paths are resolved against the working directory.
func parseSource(arg string) (typegen.Source, error) {
	var source typegen.Source
	if match := alias.FindString(arg); match != "" {
		source.Alias, arg = strings.TrimSuffix(match, "="), arg[len(match):]
	}
	if arg == "" {
		return source, errors.New("a server path or URL is required")
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
	var err error
	source.Path, err = filepath.Abs(arg)
	return source, err
}

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

// consumer selects dependencies from -s or server.json. A -s path inside dir is the project's own
// server, which supplies its identity and contract.
func consumer(dir, typed string, servers []string) (typegen.Consumer, error) {
	targets, err := typegen.DeclaredTargets(dir)
	if err != nil {
		if typed != "" && len(servers) == 0 && errors.Is(err, typegen.ErrNoRuntime) {
			if entry, _ := typegen.SourceEntry(dir); entry != "" {
				return typegen.Consumer{}, fmt.Errorf("%w (it looks like a server; did you mean -s %s?)", err, typed)
			}
		}
		return typegen.Consumer{}, err
	}
	c := typegen.Consumer{Name: filepath.Base(dir), Dir: dir, Targets: targets}
	for _, arg := range servers {
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
	if len(servers) == 0 {
		c.Sources, err = typegen.DeclaredSources(dir)
		if err != nil {
			return c, err
		}
	}
	if len(c.Sources) == 0 {
		return c, fmt.Errorf("%s has no requires in server.json; pass -s", dir)
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
		return err
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

// generateProject resolves the client directory and generates its declared slots from -s
// or server.json requires. It returns the directory for type-checking.
func generateProject(cmd *cobra.Command, servers []string, progress io.Writer) (string, error) {
	typed := cmd.Flags().Arg(0)
	dir, err := clientDir(typed)
	if err != nil {
		return "", err
	}
	c, err := consumer(dir, typed, servers)
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
	cmd := &cobra.Command{Use: "check [DIR]", Short: short, Long: "Generate typed clients for MCP servers, then type-check the project's calls to them.\n\n" + dependencyHelp, Example: `  # Generate from server.json requires, then type-check
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

const serverHelp = "A dependency, [ALIAS=]PATH or URL, paths relative to the working directory; repeatable. Without it, server.json requires is used"

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
	var verbose bool
	var contract string
	var servers []string
	generate := &cobra.Command{Use: "generate [DIR]", Short: "Generate typed clients for MCP servers", Long: "Generate typed clients for MCP servers.\n\n" + dependencyHelp, Example: generateExamples, Args: oneDir, RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("contract") {
			if len(args) > 0 || len(servers) > 0 {
				return errors.New("--contract takes one SERVER and nothing else")
			}
			return printContract(cmd, contract)
		}
		progress := io.Discard
		if verbose {
			progress = cmd.ErrOrStderr()
		}
		_, err := generateProject(cmd, servers, progress)
		return err
	}}
	generate.Flags().StringVar(&contract, "contract", "", "Print one server's MCP contract instead of generating")
	generate.Flags().StringArrayVarP(&servers, "server", "s", nil, serverHelp)
	generate.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show generation details")
	types.AddCommand(generate, newCheck("Type-check the project's calls to MCP servers"))

	return types
}
