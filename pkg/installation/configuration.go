package installation

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/holon-sh/cli/internal/clientutil"

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
	use, argumentCount, long, example := action+" VARIABLE", 1, "", configurationExample(kind, action)
	secretInput := action == "set" && kind == "secret"
	switch {
	case action == "set" && kind == "config":
		use, argumentCount = "set VARIABLE VALUE", 2
	case secretInput:
		long = "Set a secret value. On a terminal the value is read from a hidden prompt; otherwise it is read from standard input, with the trailing newline removed."
		example += "\n\n  # Pipe the value in from a script or CI\n  printenv API_KEY | holon secret set API_KEY"
	}
	return withScope(&cobra.Command{
		Use:     use,
		Short:   action + " a " + kind + " value",
		Long:    long,
		Example: example,
		Args:    cobra.ExactArgs(argumentCount),
	}, func(cmd *cobra.Command, args []string, scope configurationScope) error {
		variable, value := args[0], ""
		if !protocol.ValidEnvName(variable) {
			return fmt.Errorf("invalid variable name %q", variable)
		}
		switch {
		case argumentCount == 2:
			value = args[1]
		case secretInput:
			var err error
			if value, err = readSecret(cmd, variable); err != nil {
				return err
			}
		}
		if action == "set" && !protocol.ValidInputValue(value) {
			return errors.New("value must be single-line text without NUL characters")
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
		input = protocol.SetValueRequest{VariableRequest: variableInput, Value: value}
	}
	if err := manager.Call(ctx, ControlPlane(string(scope)), tool, input, &struct{}{}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s in %s on %s. Deployments pick this up on their next deploy.\n",
		verb, kind, variable, scope.label(), manager.client.baseURL)
	return err
}

// readSecret prompts for the value without echo on a terminal and otherwise
// reads it from standard input, so scripts can pipe it in.
// readSecret prompts without echo on a terminal and otherwise reads standard
// input, so scripts can pipe the value in.
func readSecret(cmd *cobra.Command, variable string) (string, error) {
	in := cmd.InOrStdin()
	var secret []byte
	var err error
	if file, ok := terminalFile(in); ok {
		stderr := cmd.ErrOrStderr()
		_, _ = fmt.Fprintf(stderr, "Paste %s: ", variable)
		secret, err = term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(stderr)
	} else {
		secret, err = clientutil.ReadLimited(in, maxHTTPBody)
	}
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(secret), "\r\n")
	if value == "" {
		return "", errors.New("secret value is empty")
	}
	return value, nil
}

func configurationExample(kind, action string) string {
	variable, command := "API_KEY", "holon "+kind+" "+action+" API_KEY"
	if kind == "config" {
		variable, command = "REGION", "holon config "+action+" REGION"
		if action == "set" {
			command += " us-east1"
		}
	}
	title := fmt.Sprintf("%s the %s %s for development", strings.ToUpper(action[:1])+action[1:], kind, variable)
	return fmt.Sprintf(`  # %s
  %s

  # Production, or one development session
  %s --env prod
  %s --session my-session`, title, command, command, command)
}
