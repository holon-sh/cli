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
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/spf13/cobra"
)

const sourceUsage = `SOURCE is [ALIAS=]PATH or [ALIAS=]URL: a server entry (server.py, server.ts, server.js, or the
same under src/) or a directory holding one; an MCP registry server version URL
(https://registry.modelcontextprotocol.io/v0.1/servers/NAME/versions/VERSION); or a live MCP endpoint.
ALIAS names the client (apps.ALIAS); it defaults to the server's name.`

var alias = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?=`)
var registryPath = regexp.MustCompile(`^/v0(?:\.1)?/servers/([^/]+)/versions/([^/]+)$`)

// parseSource reads one SOURCE argument. Paths are resolved against the working directory.
func parseSource(arg string) (typegen.Source, error) {
	var source typegen.Source
	if match := alias.FindString(arg); match != "" {
		source.Alias, arg = strings.TrimSuffix(match, "="), arg[len(match):]
	}
	if arg == "" {
		return source, errors.New("a source is required after ALIAS=")
	}
	if u, err := url.Parse(arg); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if m := registryPath.FindStringSubmatch(u.EscapedPath()); m != nil {
			name, err := url.PathUnescape(m[1])
			if err != nil {
				return source, err
			}
			version, err := url.PathUnescape(m[2])
			if err != nil {
				return source, err
			}
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

// consumer builds the working directory's consumer for one target. For the ui target the one
// path source inside the working directory is the own server; other targets reject such a source.
func consumer(target string, args []string) (typegen.Consumer, error) {
	dir, err := os.Getwd()
	if err != nil {
		return typegen.Consumer{}, err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return typegen.Consumer{}, err
	}
	c := typegen.Consumer{Name: filepath.Base(dir), Dir: dir, Targets: []string{target}}
	for _, arg := range args {
		source, err := parseSource(arg)
		if err != nil {
			return c, fmt.Errorf("%s: %w", arg, err)
		}
		if source.Path != "" && insideDir(source.Path, dir) {
			if target != typegen.TargetUI {
				return c, fmt.Errorf("%s is this project's own server; a project does not depend on itself (--target ui makes it the App class for a view)", arg)
			}
			if c.Server != nil {
				return c, fmt.Errorf("%s: --target ui takes exactly one own server inside %s", arg, dir)
			}
			c.Server = &source
			continue
		}
		c.Sources = append(c.Sources, source)
	}
	if target == typegen.TargetUI && c.Server == nil {
		return c, fmt.Errorf("--target ui needs the own server, a path inside %s (for example ./server.ts)", dir)
	}
	if len(args) == 0 {
		return c, errors.New("at least one SOURCE is required")
	}
	return c, nil
}

func insideDir(path, dir string) bool {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	relative, err := filepath.Rel(dir, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func validTarget(target string) error {
	switch target {
	case typegen.TargetTS, typegen.TargetUI, typegen.TargetPy:
		return nil
	case "":
		return errors.New("--target is required: ts, ui or py")
	}
	return fmt.Errorf("unknown target %q: expected ts, ui or py", target)
}

// printContract prints one source's contract document and writes no files.
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
	var target string
	check := &cobra.Command{Use: "check --target ts|ui|py SOURCE...", Short: short, Long: "Type-check the current directory against its typed clients, in a temporary copy.\n\n" + sourceUsage, RunE: func(cmd *cobra.Command, args []string) error {
		if err := validTarget(target); err != nil {
			return err
		}
		c, err := consumer(target, args)
		if err != nil {
			return err
		}
		return typegen.Check(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
	}}
	check.Flags().StringVar(&target, "target", "", "Slot to check: ts (holon-sh), ui (holon-ui) or py (holon/apps)")
	return check
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
	var target string
	var verbose, contract bool
	generate := &cobra.Command{Use: "generate --target ts|ui|py SOURCE...", Short: "Generate typed clients for MCP servers into the current directory", Long: "Generate typed clients for MCP servers into the current directory's SDK slot.\n\n" + sourceUsage, RunE: func(cmd *cobra.Command, args []string) error {
		if contract {
			if target != "" {
				return errors.New("--contract prints one source's contract and takes no --target")
			}
			if len(args) != 1 {
				return errors.New("--contract takes exactly one SOURCE")
			}
			return printContract(cmd, args[0])
		}
		if err := validTarget(target); err != nil {
			return err
		}
		c, err := consumer(target, args)
		if err != nil {
			return err
		}
		progress := io.Discard
		if verbose {
			progress = cmd.ErrOrStderr()
		}
		return typegen.Generate(cmd.Context(), []typegen.Consumer{c}, typegen.Options{Stdout: progress, Stderr: cmd.ErrOrStderr()})
	}}
	generate.Flags().StringVar(&target, "target", "", "Slot to write: ts (holon-sh), ui (holon-ui) or py (holon/apps)")
	generate.Flags().BoolVar(&contract, "contract", false, "Print one source's MCP contract instead of generating")
	generate.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show generation details")
	types.AddCommand(generate)
	types.AddCommand(newCheck("Type-check the current directory against its typed clients"))

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
