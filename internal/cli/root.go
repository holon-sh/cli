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
	"strings"
)

func NewCommand(version string) *cobra.Command {
	var project string
	root := &cobra.Command{Use: "holon", Short: "Generate and check portable MCP application types", Version: version + " (standalone types CLI)", SilenceUsage: true, SilenceErrors: true}
	root.SetVersionTemplate("holon {{.Version}}\n")
	root.PersistentFlags().StringVarP(&project, "project", "p", "", "Path to holon.yaml")
	types := &cobra.Command{Use: "types", Short: "Generate clients and inspect MCP contracts"}
	root.AddCommand(types)
	var noInstall, quiet bool
	generate := &cobra.Command{Use: "generate [APP...]", Short: "Generate types in installed SDK slots", RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := typegen.LoadManifest(project)
		if err != nil {
			return err
		}
		stdout := cmd.OutOrStdout()
		if quiet {
			stdout = io.Discard
		}
		return typegen.Generate(cmd.Context(), manifest, typegen.Options{Apps: args, NoInstall: noInstall, Stdout: stdout, Stderr: cmd.ErrOrStderr()})
	}}
	generate.Flags().BoolVar(&noInstall, "no-install", false, "Print missing runtime installation commands without running them")
	generate.Flags().BoolVar(&quiet, "quiet", false, "Suppress generation progress")
	types.AddCommand(generate)
	var noPublished bool
	check := &cobra.Command{Use: "check [APP...]", Short: "Check typed contracts in temporary source copies", RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := typegen.LoadManifest(project)
		if err != nil {
			return err
		}
		return typegen.Check(cmd.Context(), manifest, typegen.Options{Apps: args, NoPublished: noPublished, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
	}}
	check.Flags().BoolVar(&noPublished, "no-published", false, "Skip comparison with published contracts")
	root.AddCommand(check)
	var url, name, release string
	contract := &cobra.Command{Use: "contract [APP]", Short: "Print a language-neutral MCP contract document", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var metadata *mcpcontract.Metadata
		if url != "" {
			if len(args) != 0 {
				return errors.New("APP and --url are mutually exclusive")
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
			m, err := typegen.LoadManifest(project)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				for _, app := range m.Apps {
					if app.Path != "" {
						args = append(args, app.Name)
					}
				}
				if len(args) != 1 {
					return errors.New("choose one APP to print its contract")
				}
			}
			metadata, err = typegen.Contract(cmd.Context(), m, args[0], typegen.Options{Stderr: cmd.ErrOrStderr()})
			if err != nil {
				return err
			}
			if metadata == nil || metadata.Contract == nil {
				return fmt.Errorf("%s has no statically extracted contract", args[0])
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
		var data []byte
		var err error
		if len(args) == 1 && args[0] != "-" {
			data, err = os.ReadFile(args[0])
		} else {
			data, err = io.ReadAll(cmd.InOrStdin())
		}
		if err != nil {
			return err
		}
		var request mcptype.GenerateRequest
		decoder := json.NewDecoder(strings.NewReader(string(data)))
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
			path := filepath.Join(out, filepath.FromSlash(name))
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
