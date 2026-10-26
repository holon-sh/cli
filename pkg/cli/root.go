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
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/spf13/cobra"
)

func examples(command string) string {
	return `  # Generate for the current project, from server.json requires
  ` + command + `

  # Another project
  ` + command + ` ../checkout

  # Explicit dependencies: a server directory, a server entry, an MCP registry server, a remote MCP server named docs
  ` + command + ` -s ../orders
  ` + command + ` -s ../orders/server.py
  ` + command + ` -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
  ` + command + ` -s docs=https://mcp.context7.com/mcp

  # Print one server's contract
  ` + command + ` --contract ../orders`
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

func oneDir(_ *cobra.Command, args []string) error {
	if len(args) > 1 {
		return errors.New("one DIR at most; dependencies are -s values")
	}
	return nil
}

// clientDir resolves the optional client directory. typed is empty when DIR was omitted.
func clientDir(args []string) (dir, typed string, err error) {
	path := "."
	if len(args) == 1 {
		path, typed = args[0], args[0]
	}
	dir, err = filepath.Abs(path)
	if err != nil {
		return "", typed, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", typed, fmt.Errorf("%s is not a directory", path)
	}
	dir, err = filepath.EvalSymlinks(dir)
	return dir, typed, err
}

// consumer selects dependencies from -s or server.json and finds the ui target's own server.
func consumer(dir, typed string, servers []string) (typegen.Consumer, error) {
	targets, err := typegen.DeclaredTargets(dir)
	if err != nil {
		if typed != "" && len(servers) == 0 && strings.HasPrefix(err.Error(), dir+" declares no runtime:") {
			if own, _ := typegen.OwnServer(dir); own != nil {
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
			return c, fmt.Errorf("%s is inside %s; the project's own server is found automatically, pass only dependencies with -s", arg, dir)
		}
		c.Sources = append(c.Sources, source)
	}
	if len(servers) == 0 {
		c.Sources, err = typegen.DeclaredSources(dir)
		if err != nil {
			return c, err
		}
		if len(c.Sources) == 0 && !slices.Contains(targets, typegen.TargetUI) {
			return c, fmt.Errorf("%s has no requires in server.json; pass -s", dir)
		}
	}
	if slices.Contains(targets, typegen.TargetUI) {
		c.Server, err = typegen.OwnServer(dir)
		if err != nil {
			return c, err
		}
		if c.Server == nil {
			return c, fmt.Errorf("%s depends on holon-ui but has no server entry (expected one of %s)", dir, strings.Join(typegen.EntryFiles(), ", "))
		}
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
	var servers []string
	var noGenerate bool
	cmd := &cobra.Command{Use: "check [DIR]", Short: short, Long: "Generate the project's typed clients from server.json requires (or -s), then type-check its calls to them.\nWith --no-generate, type-check the existing clients only: no network, no install, no writes.", Example: `  # Generate from server.json requires, then type-check
  holon types check

  # Type-check the existing clients only (no network, no writes)
  holon types check --no-generate

  # Explicit dependencies, as for generate
  holon types check -s orders=../orders`, Args: oneDir, RunE: func(cmd *cobra.Command, args []string) error {
		if noGenerate && len(servers) > 0 {
			return errors.New("--no-generate type-checks the existing clients; -s has no effect")
		}
		dir, typed, err := clientDir(args)
		if err != nil {
			return err
		}
		if !noGenerate {
			c, err := consumer(dir, typed, servers)
			if err != nil {
				return err
			}
			if err := typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: io.Discard, Stderr: cmd.ErrOrStderr()}); err != nil {
				return err
			}
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
	generate := &cobra.Command{Use: "generate [DIR]", Short: "Generate typed clients for MCP servers", Long: "Generate typed clients for the project's MCP servers, from server.json requires or the -s list. Generation may install the declared holon-sh/holon-ui package and rewrites the generated/ slot.", Example: examples("holon types generate"), Args: oneDir, RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("contract") {
			if len(args) > 0 || len(servers) > 0 {
				return errors.New("--contract takes one SERVER and nothing else")
			}
			return printContract(cmd, contract)
		}
		dir, typed, err := clientDir(args)
		if err != nil {
			return err
		}
		c, err := consumer(dir, typed, servers)
		if err != nil {
			return err
		}
		progress := io.Discard
		if verbose {
			progress = cmd.ErrOrStderr()
		}
		return typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: progress, Stderr: cmd.ErrOrStderr()})
	}}
	generate.Flags().StringVar(&contract, "contract", "", "Print one server's MCP contract instead of generating")
	generate.Flags().StringArrayVarP(&servers, "server", "s", nil, serverHelp)
	generate.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show generation details")
	types.AddCommand(generate)
	types.AddCommand(newCheck("Type-check the project's calls to MCP servers"))

	// Plumbing for the SDK repositories' test fixtures: compile one generate request
	// (the JSON form of mcptype.GenerateRequest, from a file or stdin) into a directory.
	var out string
	compile := &cobra.Command{Use: "compile [REQUEST.json]", Short: "Compile a generate request into package files", Hidden: true, Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		input := cmd.InOrStdin()
		if len(args) == 1 && args[0] != "-" {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			input = file
		}
		var request mcptype.GenerateRequest
		decoder := json.NewDecoder(input)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return fmt.Errorf("generate request: %w", err)
		}
		files, err := mcptype.Generate(cmd.Context(), request)
		if err != nil {
			return err
		}
		for name, content := range files {
			if !filepath.IsLocal(name) {
				return fmt.Errorf("generated file name %q escapes the output directory", name)
			}
			path := filepath.Join(out, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				return err
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "compiled %d file(s) into %s\n", len(files), out)
		return nil
	}}
	compile.Flags().StringVar(&out, "out", ".", "Directory that receives the generated files")
	types.AddCommand(compile)
	return types
}
