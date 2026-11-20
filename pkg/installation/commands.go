package installation

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/utils/ptr"

	"github.com/holon-sh/protocol"
)

const appsHelp = "APP is an app in holon.yaml (or -f FILE), else the directory ./APP with a server.json. " +
	"APP=SOURCE names an app's source: a directory, an MCP Registry server version URL, or an https MCP endpoint. " +
	"Without APP arguments, every app in holon.yaml runs, or without one every ./*/server.json directory.\n\n" +
	"Apps outside holon.yaml take --public or --oauth; one already in production keeps its access rules without them."

func Commands() []*cobra.Command {
	return newCommands(commandConfig{watch: watchFiles})
}

func newCommands(config commandConfig) []*cobra.Command {
	installationCommands := []*cobra.Command{
		{
			Use:   "login [ORIGIN]",
			Short: "Sign in and select a Holon installation",
			Example: `  # Sign in and select an installation
  holon login acme.holonlabs.io

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
				// Reading the identity proves the new credentials work before the installation is selected.
				if manager.info.OAuth != nil {
					var caller protocol.Caller
					if err := manager.Call(cmd.Context(), protocol.DevPlane, protocol.ToolGetIdentity, struct{}{}, &caller); err != nil {
						return err
					}
					identity := ptr.Deref(caller.Identity, protocol.CallerIdentity{})
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Signed in to %s as %s\n", manager.client.baseURL,
						cmp.Or(ptr.Deref(identity.Profile, protocol.CallerProfile{}).Email, identity.Subject))
				}
				return saveOrigin(config.Auth, manager.client.baseURL)
			},
		},
		{
			Use:   "logout",
			Short: "Remove credentials for the selected Holon installation",
			Example: `  # Sign out of the selected installation
  holon logout

  # Sign out of another installation
  holon logout --origin acme.holonlabs.io`,
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
		newSessionCommand(&config),
		newDeployCommand(&config),
		newLogsCommand(&config),
		newSecretCommand(&config),
		newConfigCommand(&config),
		newAppCommand(&config),
		newAccessCommand(&config),
		newEgressCommand(&config),
	}
	for _, cmd := range installationCommands {
		cmd.PersistentFlags().StringVar(&config.Auth.Origin, "origin", config.Auth.Origin, "Holon installation origin (default HOLON_ORIGIN or saved selection)")
		cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
			config.Auth.Output = cmd.ErrOrStderr()
			_, config.Auth.Interactive = terminalFile(config.Auth.Output)
			if !cmd.Flags().Changed("origin") {
				return nil
			}
			_, err := canonicalOrigin(config.Auth.Origin)
			return err
		}
	}
	return installationCommands
}

func newDevCommand(config *commandConfig) *cobra.Command {
	const maxTTL = protocol.MaxTTLSeconds * time.Second
	var detached bool
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "dev [APP[=SOURCE]...]",
		Short: "Run a development session",
		Long: "Run apps and their required dependencies. Each app uses its mount, /<app-name>/ by default, on the session host. " +
			"Every change type-checks the app's calls to the apps it requires. " +
			"Use --verbose to show build output, timings, app logs, and MCP calls. " +
			"You can also view app logs and MCP calls with holon logs.\n\n" + appsHelp,
		Example: `  # Run every app in holon.yaml
  holon dev

  # Run an app and the apps it requires
  holon dev chat

  # Run an app from a directory, an MCP Registry server version URL, or an https MCP endpoint
  holon dev chat=../chat
  holon dev hf=https://huggingface.co/mcp

  # Exit once the session is ready and print it as JSON
  holon dev --detach --json

  # Create the detached session cli-pr-42, or update it in place so its address stays the same
  holon dev --detach --session cli-pr-42

  # End it before its --ttl runs out
  holon session delete cli-pr-42`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if detached {
				if ttl < time.Second || ttl > maxTTL || ttl%time.Second != 0 {
					return fmt.Errorf("--ttl must be whole seconds between 1s and %dh", maxTTL/time.Hour)
				}
				if cmd.Flags().Changed("session") && !protocol.ValidSessionName(config.devSession) {
					return errors.New("--session requires a development session name")
				}
				config.devTTLSeconds = new(int64(ttl / time.Second))
			} else if config.jsonOutput {
				return errors.New("--json requires --detach")
			} else if cmd.Flags().Changed("ttl") {
				return errors.New("--ttl requires --detach")
			} else if cmd.Flags().Changed("session") {
				return errors.New("--session requires --detach")
			}
			return runDev(cmd.Context(), config.apps, cmd.OutOrStdout(), cmd.ErrOrStderr(), *config)
		},
	}
	cmd.Flags().BoolVar(&detached, "detach", false, "Exit once the session is ready and keep it for --ttl from this run")
	cmd.Flags().BoolVar(&config.jsonOutput, "json", false, "Write the ready API session as JSON (requires --detach)")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, fmt.Sprintf("Detached session lifetime (whole seconds, at most %dh)", maxTTL/time.Hour))
	cmd.Flags().StringVar(&config.devSession, "session", "", "Name the detached session to create, or to update in place (requires --detach)")
	return withProject(withProgress(cmd, config, "Done"), config)
}

func newSessionCommand(config *commandConfig) *cobra.Command {
	return commandGroup("session", "Delete development sessions", &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a development session",
		Long:  "Delete a development session and wait until it is gone. Deleting a session that does not exist succeeds.",
		Example: `  # Delete the development session cli-pr-42
  holon session delete cli-pr-42`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !protocol.ValidSessionName(args[0]) {
				return fmt.Errorf("invalid session name %q", args[0])
			}
			manager, err := NewClient(cmd.Context(), config.Auth)
			if err != nil {
				return err
			}
			if err := manager.DeleteSession(cmd.Context(), args[0], "", false); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s.\n", configurationScope(args[0]).label())
			return err
		},
	})
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
			return runDeploy(cmd.Context(), config.apps, cmd.OutOrStdout(), *config)
		},
	}
	return withProject(withProgress(cmd, config, "Deployment ready"), config)
}

func withProject(cmd *cobra.Command, config *commandConfig) *cobra.Command {
	var public bool
	var oauth *protocol.OAuthRequirement
	cmd.Flags().StringVarP(&config.file, "file", "f", config.file, "App manifest file (default holon.yaml or holon.yml)")
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
