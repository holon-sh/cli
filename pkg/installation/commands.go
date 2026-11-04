package installation

import (
	"errors"
	"fmt"
	"slices"
	"time"

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
		newDevCommand(&config),
		newDeployCommand(&config),
		newConfigurationCommand("secret", &config),
		newConfigurationCommand("config", &config),
		newEgressCommand(&config),
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

func newDevCommand(config *commandConfig) *cobra.Command {
	var detached bool
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "dev [APP...]",
		Short: "Run a development session",
		Long: "Run all apps in the manifest, or the named apps and their required dependencies. Each app uses /<app-name>/ on the session host.\n\n" +
			"Use --project (-p) to select a configuration file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if detached {
				if ttl < time.Second || ttl > 24*time.Hour || ttl%time.Second != 0 {
					return errors.New("--ttl must be whole seconds between 1s and 24h")
				}
				config.devTTLSeconds = new(int64(ttl / time.Second))
			} else if config.jsonOutput {
				return errors.New("--json requires --detach")
			} else if cmd.Flags().Changed("ttl") {
				return errors.New("--ttl requires --detach")
			}
			return runDev(cmd.Context(), args, cmd.OutOrStdout(), cmd.ErrOrStderr(), *config)
		},
	}
	cmd.Flags().BoolVar(&detached, "detach", false, "Exit after readiness and retain the session until its fixed expiry")
	cmd.Flags().BoolVar(&config.check, "check", false, "Type-check apps before starting or updating the session")
	cmd.Flags().BoolVar(&config.jsonOutput, "json", false, "Write the ready API session as JSON (requires --detach)")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "Detached session lifetime (whole seconds, at most 24h)")
	return withProject(withProgress(cmd, config, "Done"), config)
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
		if config.jsonOutput {
			renderedOut = stdout
		}
		config.output = output
		config.Auth.Output = renderedErr
		cmd.SetOut(renderedOut)
		cmd.SetErr(renderedErr)
		defer func() {
			result = errors.Join(result, output.finish(errors.Join(result, cmd.Context().Err()), message))
		}()
		return run(cmd, args)
	}
	return cmd
}
