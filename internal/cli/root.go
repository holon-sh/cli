package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/spf13/cobra"
	"io"
	"os"
	"path/filepath"
)

// appDirs returns the application directories: args, or the working directory when there are none.
func appDirs(args []string) []string {
	if len(args) == 0 {
		return []string{"."}
	}
	return args
}

func NewCommand(version string) *cobra.Command {
	root := &cobra.Command{Use: "holon", Short: "Generate and check typed MCP clients for MCP server directories", Version: version + " (standalone types CLI)", SilenceUsage: true, SilenceErrors: true}
	root.SetVersionTemplate("holon {{.Version}}\n")
	types := &cobra.Command{Use: "types", Short: "Generate clients and inspect MCP contracts"}
	root.AddCommand(types)
	var noInstall, quiet bool
	generate := &cobra.Command{Use: "generate [DIR...]", Short: "Generate types in installed SDK slots", RunE: func(cmd *cobra.Command, args []string) error {
		manifest, names, err := typegen.Directories(appDirs(args))
		if err != nil {
			return err
		}
		stdout := cmd.OutOrStdout()
		if quiet {
			stdout = io.Discard
		}
		return typegen.Generate(cmd.Context(), manifest, typegen.Options{Apps: names, NoInstall: noInstall, Stdout: stdout, Stderr: cmd.ErrOrStderr()})
	}}
	generate.Flags().BoolVar(&noInstall, "no-install", false, "Print missing runtime installation commands without running them")
	generate.Flags().BoolVar(&quiet, "quiet", false, "Suppress generation progress")
	types.AddCommand(generate)
	check := &cobra.Command{Use: "check [DIR...]", Short: "Type-check apps against their typed contracts in temporary source copies", RunE: func(cmd *cobra.Command, args []string) error {
		manifest, names, err := typegen.Directories(appDirs(args))
		if err != nil {
			return err
		}
		return typegen.Check(cmd.Context(), manifest, typegen.Options{Apps: names, NoPublished: true, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
	}}
	root.AddCommand(check)
	var url, name, release string
	contract := &cobra.Command{Use: "contract [DIR] | --url URL --name NAME --version VERSION", Short: "Print a language-neutral MCP contract document", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var metadata *mcpcontract.Metadata
		if url != "" {
			if len(args) != 0 {
				return errors.New("DIR and --url are mutually exclusive")
			}
			if name == "" || release == "" {
				return errors.New("--url requires --name and --version")
			}
			var err error
			metadata, err = typegen.LiveContract(cmd.Context(), url, mcpcontract.Identity{Name: name, Version: release}, nil)
			if err != nil {
				return err
			}
		} else {
			if name != "" || release != "" {
				return errors.New("--name and --version require --url")
			}
			m, names, err := typegen.Directories(appDirs(args))
			if err != nil {
				return err
			}
			app := names[0]
			metadata, err = typegen.Contract(cmd.Context(), m, app, typegen.Options{Stderr: cmd.ErrOrStderr()})
			if err != nil {
				return err
			}
			if metadata == nil || metadata.Contract == nil {
				return fmt.Errorf("%s has no statically extracted contract", app)
			}
		}
		// Provider documents contain one contract, without caller expectations.
		document := struct {
			Version  int                   `json:"version"`
			Identity mcpcontract.Identity  `json:"identity"`
			Contract *mcpcontract.Contract `json:"contract"`
		}{mcpcontract.Version, metadata.Identity, metadata.Contract}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(document)
	}}
	contract.Flags().StringVar(&url, "url", "", "Fetch schemas from a Streamable HTTP MCP endpoint")
	contract.Flags().StringVar(&name, "name", "", "Registry server identity (namespace/name)")
	contract.Flags().StringVar(&release, "version", "", "Exact provider version")
	types.AddCommand(contract)

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
	return root
}
