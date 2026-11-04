package installation

import (
	"errors"
	"fmt"

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
