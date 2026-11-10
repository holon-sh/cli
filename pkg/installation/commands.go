package installation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/holon-sh/cli/internal/kiln"
	"github.com/holon-sh/protocol"
)

const appsHelp = "APP is an app in holon.yaml (or -p FILE), else the directory ./APP with a server.json. " +
	"APP=SOURCE names an app's source: a directory, an MCP Registry server version URL, or an https MCP endpoint. " +
	"Without APP arguments, every app in holon.yaml runs, or without one every ./*/server.json directory.\n\n" +
	"Apps outside holon.yaml take --public or --oauth; one already in production keeps its access rules without them."

func Commands() []*cobra.Command {
	return newCommands(commandConfig{})
}

func newCommands(config commandConfig) []*cobra.Command {
	config.Auth.Env = append(config.Auth.Env, config.Env...)
	installationCommands := []*cobra.Command{
		{
			Use:   "login [ORIGIN]",
			Short: "Sign in and select a Holon installation",
			Example: `  # Sign in and select an installation
  holon login https://acme.cloud.holon-labs.dev

  # Sign in again to the selected installation
  holon login`,
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
			Example: `  # Sign out of the selected installation
  holon logout

  # Sign out of another installation
  holon logout --origin https://acme.cloud.holon-labs.dev`,
			Args: cobra.NoArgs,
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
		newAppCommand(&config),
		newAccessCommand(&config),
		newEgressCommand(&config),
	}
	for _, cmd := range installationCommands {
		cmd.PersistentFlags().StringVar(&config.Auth.Origin, "origin", config.Auth.Origin, "Holon installation origin (default HOLON_ORIGIN or saved selection)")
		cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
			config.Auth.Output = cmd.ErrOrStderr()
			_, config.Auth.Interactive = terminalFile(config.Auth.Output)
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
		Use:   "dev [APP[=SOURCE]...]",
		Short: "Run a development session",
		Long: "Run apps and their required dependencies. Each app uses /<app-name>/ on the session host. " +
			"Every change type-checks the app's calls to the apps it requires.\n\n" + appsHelp,
		Example: `  # Run every app in holon.yaml
  holon dev

  # Run an app and the apps it requires
  holon dev chat

  # Run an app from a directory, an MCP Registry server version URL, or an https MCP endpoint
  holon dev chat=../chat
  holon dev hf=https://huggingface.co/mcp

  # Exit once the session is ready and print it as JSON
  holon dev --detach --json`,
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
			return runDev(cmd.Context(), config.apps, cmd.OutOrStdout(), cmd.ErrOrStderr(), *config)
		},
	}
	cmd.Flags().BoolVar(&detached, "detach", false, "Exit after readiness and retain the session until its fixed expiry")
	cmd.Flags().BoolVar(&config.jsonOutput, "json", false, "Write the ready API session as JSON (requires --detach)")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "Detached session lifetime (whole seconds, at most 24h)")
	return withProject(withProgress(cmd, config, "Done"), config)
}

func newDeployCommand(config *commandConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [APP[=SOURCE]...]",
		Short: "Deploy apps to production",
		Long: "Deploy every app, or the named apps and their required dependencies, to production. " +
			"Production apps this deploy does not name keep running; holon app remove removes one.\n\n" + appsHelp,
		Example: `  # Deploy every app in holon.yaml
  holon deploy

  # Deploy an app and the apps it requires
  holon deploy chat

  # Deploy an app outside holon.yaml that requires sign-in, with or without scopes
  holon deploy notes --oauth
  holon deploy notes --oauth=app:notes:read

  # Deploy an app outside holon.yaml that anyone can call
  holon deploy notes --public`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDeploy(cmd.Context(), config.apps, cmd.OutOrStdout(), cmd.ErrOrStderr(), *config)
		},
	}
	return withProject(withProgress(cmd, config, "Deployment ready"), config)
}

func withProject(cmd *cobra.Command, config *commandConfig) *cobra.Command {
	var public bool
	var oauth *protocol.OAuthRequirement
	cmd.Flags().StringVarP(&config.file, "project", "p", config.file, "Project config file (default holon.yaml or holon.yml)")
	cmd.Flags().BoolVar(&public, "public", false, "Let anyone call the apps outside holon.yaml")
	cmd.Flags().BoolFunc("oauth", "Require sign-in for the apps outside holon.yaml; --oauth=SCOPE,... also requires the scopes", func(value string) error {
		oauth = &protocol.OAuthRequirement{}
		if value != "true" {
			oauth.Scopes = strings.Split(value, ",")
		}
		return nil
	})
	cmd.MarkFlagsMutuallyExclusive("public", "oauth")
	cmd.PreRunE = func(_ *cobra.Command, args []string) error {
		if !kiln.Linked {
			return kiln.ErrNotLinked
		}
		manifest, err := resolveManifestFile(config.file)
		if err != nil {
			return err
		}
		if public || oauth != nil {
			manifest.access = &protocol.AppAccess{Server: &protocol.AccessRule{OAuth: oauth}}
		}
		config.apps, err = manifest.selectApps(args)
		config.manifest = manifest
		return err
	}
	return cmd
}

func withProgress(cmd *cobra.Command, config *commandConfig, message string) *cobra.Command {
	cmd.Flags().BoolVarP(&config.verbose, "verbose", "v", false, "Show verbose output")
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
