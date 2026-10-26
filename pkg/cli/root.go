// Package cli provides the portable Holon commands for standalone and extended executables.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/spf13/cobra"
)

// appDirs returns the application directories: args, or the working directory when there are none.
func appDirs(args []string) []string {
	if len(args) == 0 {
		return []string{"."}
	}
	return args
}

// printContract prints the app's own contract document, without caller expectations, and writes no files.
func printContract(cmd *cobra.Command, manifest typegen.Manifest, app string) error {
	metadata, err := typegen.Contract(cmd.Context(), manifest, app, typegen.Options{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return err
	}
	if metadata == nil || metadata.Contract == nil {
		return fmt.Errorf("%s has no statically extracted contract", app)
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
	return &cobra.Command{Use: "check [DIR...]", Short: short, Long: "Type-check apps", RunE: func(cmd *cobra.Command, args []string) error {
		manifest, names, err := typegen.Directories(appDirs(args))
		if err != nil {
			return err
		}
		return typegen.Check(cmd.Context(), manifest, typegen.Options{Apps: names, NoPublished: true, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
	}}
}

// NewCommand builds the standalone CLI, including the check shortcut.
func NewCommand(version string) *cobra.Command {
	root := &cobra.Command{Use: "holon", Short: "Generate and check typed MCP clients for MCP server directories", Version: version + " (standalone types CLI)", SilenceUsage: true, SilenceErrors: true}
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
	generate := &cobra.Command{Use: "generate [DIR...]", Short: "Generate typed clients for MCP servers", RunE: func(cmd *cobra.Command, args []string) error {
		if contract && len(args) > 1 {
			return errors.New("--contract takes at most one DIR")
		}
		manifest, names, err := typegen.Directories(appDirs(args))
		if err != nil {
			return err
		}
		if contract {
			return printContract(cmd, manifest, manifest.Apps[0].Name)
		}
		progress := io.Discard
		if verbose {
			progress = cmd.ErrOrStderr()
		}
		return typegen.Generate(cmd.Context(), manifest, typegen.Options{Apps: names, Stdout: progress, Stderr: cmd.ErrOrStderr()})
	}}
	generate.Flags().BoolVar(&contract, "contract", false, "Output the app's MCP contract")
	generate.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show generation details")
	types.AddCommand(generate)
	types.AddCommand(newCheck("Type-check apps"))

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
