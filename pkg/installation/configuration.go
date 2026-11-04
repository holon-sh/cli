package installation

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/holon-sh/protocol"
)

type configurationScope string

func withScope(cmd *cobra.Command, run func(*cobra.Command, []string, configurationScope) error) *cobra.Command {
	environment, session := "dev", ""
	cmd.Flags().StringVar(&environment, "env", environment, "Target environment: dev or prod")
	cmd.Flags().StringVar(&session, "session", "", "Target development session by name")
	cmd.MarkFlagsMutuallyExclusive("env", "session")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !slices.Contains([]string{"dev", "prod"}, environment) {
			return errors.New("--env must be dev or prod")
		}
		if session != "" && (!protocol.ValidDNSLabel(session) || session == protocol.ProductionName) {
			return errors.New("--session requires a development session name")
		}
		if environment == "prod" {
			return run(cmd, args, protocol.ProductionName)
		}
		return run(cmd, args, configurationScope(session))
	}
	return cmd
}

func (scope configurationScope) session() string {
	if scope == protocol.ProductionName {
		return ""
	}
	return string(scope)
}

func (scope configurationScope) label() string {
	switch scope {
	case protocol.ProductionName:
		return "production"
	case "":
		return "developer-wide dev"
	default:
		return "dev session " + string(scope)
	}
}

func commandGroup(use, short string, commands ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(commands...)
	return cmd
}

func newConfigurationCommand(kind string, config *commandConfig) *cobra.Command {
	return commandGroup(kind, "Manage "+kind+" values",
		newConfigurationActionCommand(kind, "set", config),
		newConfigurationActionCommand(kind, "delete", config),
	)
}

func newConfigurationActionCommand(kind, action string, config *commandConfig) *cobra.Command {
	use, argumentCount := action+" VARIABLE", 1
	if action == "set" && kind == "config" {
		use, argumentCount = "set VARIABLE VALUE", 2
	}
	return withScope(&cobra.Command{
		Use:   use,
		Short: action + " a " + kind + " value",
		Args:  cobra.ExactArgs(argumentCount),
	}, func(cmd *cobra.Command, args []string, scope configurationScope) error {
		variable, value := args[0], ""
		if !protocol.ValidEnvName(variable) {
			return fmt.Errorf("invalid variable name %q", variable)
		}
		if argumentCount == 2 {
			value = args[1]
		}
		return runConfiguration(cmd, kind, action, scope, variable, value, config.Auth)
	})
}

func runConfiguration(cmd *cobra.Command, kind, action string, scope configurationScope, variable, value string, auth ClientConfig) error {
	ctx := cmd.Context()
	manager, err := NewClient(ctx, auth)
	if err != nil {
		return err
	}
	tool, verb := protocol.ToolDeleteValue, "Deleted"
	variableInput := protocol.VariableRequest{Session: scope.session(), Kind: kind, Variable: variable}
	var input any = variableInput
	if action == "set" {
		tool, verb = protocol.ToolSetValue, "Stored"
		if kind == "secret" {
			input, ok := terminalFile(cmd.InOrStdin())
			if !ok {
				return errors.New("secret input requires a terminal")
			}
			stderr := cmd.ErrOrStderr()
			if _, err := fmt.Fprintf(stderr, "Paste %s: ", variable); err != nil {
				return err
			}
			secret, err := term.ReadPassword(int(input.Fd()))
			_, _ = fmt.Fprintln(stderr)
			if err != nil {
				return err
			}
			value = string(secret)
		}
		if !protocol.ValidInputValue(value) {
			return errors.New("value must be single-line text without NUL characters")
		}
		input = protocol.SetValueRequest{VariableRequest: variableInput, Value: value}
	}
	if err := manager.Call(ctx, ControlPlane(string(scope)), tool, input, &struct{}{}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s in %s on %s. Deployments pick this up on their next deploy.\n",
		verb, kind, variable, scope.label(), manager.client.baseURL)
	return err
}
