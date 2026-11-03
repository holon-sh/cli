package cloud

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"
)

func Commands() []*cobra.Command {
	return newCommands(commandConfig{})
}

func newCommands(config commandConfig) []*cobra.Command {
	config.Auth.Env = append(config.Auth.Env, config.Env...)
	installationCommands := []*cobra.Command{
		{
			Use:   "login [ORIGIN]",
			Short: "Sign in and select a Holon installation",
			Args: cobra.MatchAll(cobra.MaximumNArgs(1), func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return nil
				}
				if cmd.Flags().Changed("origin") {
					return errors.New("login ORIGIN cannot be combined with --origin")
				}
				return cmd.Flags().Set("origin", args[0])
			}),
			RunE: func(cmd *cobra.Command, _ []string) error {
				manager, err := NewClient(cmd.Context(), config.Auth)
				if err != nil {
					return err
				}
				if err := manager.loginInteractive(cmd.Context()); err != nil {
					return err
				}
				if err := saveOrigin(config.Auth, manager.client.baseURL); err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "Selected Holon installation "+manager.client.baseURL)
				return err
			},
		},
		{
			Use:   "logout",
			Short: "Remove credentials for the selected Holon installation",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				manager, err := newLocalAuthManager(config.Auth)
				if err != nil {
					return err
				}
				return manager.logout(cmd.Context())
			},
		},
		newDeployCommand(&config),
		newConfigurationCommand("secret", &config),
		newConfigurationCommand("config", &config),
	}
	for _, cmd := range installationCommands {
		cmd.PersistentFlags().StringVar(&config.Auth.Origin, "origin", config.Auth.Origin, "Holon installation origin (default HOLON_ORIGIN or saved selection)")
		cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
			config.Auth.Output = cmd.ErrOrStderr()
			if cmd.Flags().Changed("origin") && config.Auth.Origin == "" {
				return errors.New("public origin is invalid")
			}
			return nil
		}
	}
	return installationCommands
}

func newDeployCommand(config *commandConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [APP...]",
		Short: "Replace production or update selected applications",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(cmd.Context(), args, cmd.OutOrStdout(), cmd.ErrOrStderr(), *config)
		},
	}
	return withProject(withProgress(cmd, config, "Deployment ready"), config)
}

func withProject(cmd *cobra.Command, config *commandConfig) *cobra.Command {
	cmd.Flags().StringVarP(&config.file, "project", "p", config.file, "Project config file (default holon.yaml or holon.yml)")
	cmd.PreRunE = func(_ *cobra.Command, args []string) error {
		manifest, err := resolveManifestFile(config.file)
		if err != nil {
			return err
		}
		for _, name := range args {
			if !slices.ContainsFunc(manifest.Apps, func(app manifestApp) bool { return app.Name == name }) {
				return fmt.Errorf("unknown app %q in %s", name, manifest.file)
			}
		}
		config.manifest = manifest
		return nil
	}
	return cmd
}

func withProgress(cmd *cobra.Command, config *commandConfig, message string) *cobra.Command {
	cmd.Flags().BoolVarP(&config.verbose, "verbose", "v", false, "Show detailed Docker build output")
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) (result error) {
		stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
		output, renderedOut, renderedErr := newProgressOutput(stdout, stderr, config.verbose, config.clock())
		config.output = output
		config.Auth.Output = renderedErr
		cmd.SetOut(renderedOut)
		cmd.SetErr(renderedErr)
		defer func() {
			result = errors.Join(result, output.finish(errors.Join(result, cmd.Context().Err()), message))
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			config.Auth.Output = stderr
			config.output = nil
		}()
		return run(cmd, args)
	}
	return cmd
}
