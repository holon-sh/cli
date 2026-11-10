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

func newSecretCommand(config *commandConfig) *cobra.Command {
	return commandGroup("secret", "Manage secret values",
		newValueCommand("secret", &cobra.Command{
			Use:   "set VARIABLE",
			Short: "set a secret value",
			Long:  "Set a secret value. On a terminal the value is read from a hidden prompt; otherwise it is read from standard input, with the trailing newline removed.",
			Example: scopedExample("Set the secret API_KEY for development", "holon secret set API_KEY") + `

  # Pipe the value in from a script or CI
  printenv API_KEY | holon secret set API_KEY`,
			Args: cobra.ExactArgs(1),
		}, func(cmd *cobra.Command, args []string) (string, error) {
			return readSecret(cmd, args[0])
		}, config),
		newValueCommand("secret", &cobra.Command{
			Use:     "delete VARIABLE",
			Short:   "delete a secret value",
			Example: scopedExample("Delete the secret API_KEY for development", "holon secret delete API_KEY"),
			Args:    cobra.ExactArgs(1),
		}, nil, config),
	)
}

func newConfigCommand(config *commandConfig) *cobra.Command {
	return commandGroup("config", "Manage config values",
		newValueCommand("config", &cobra.Command{
			Use:     "set VARIABLE VALUE",
			Short:   "set a config value",
			Example: scopedExample("Set the config REGION for development", "holon config set REGION us-east1"),
			Args:    cobra.ExactArgs(2),
		}, func(_ *cobra.Command, args []string) (string, error) {
			return args[1], nil
		}, config),
		newValueCommand("config", &cobra.Command{
			Use:     "delete VARIABLE",
			Short:   "delete a config value",
			Example: scopedExample("Delete the config REGION for development", "holon config delete REGION"),
			Args:    cobra.ExactArgs(1),
		}, nil, config),
	)
}

// newValueCommand stores the value that value returns, or deletes the variable
// when value is nil.
func newValueCommand(kind string, cmd *cobra.Command, value func(*cobra.Command, []string) (string, error), config *commandConfig) *cobra.Command {
	return withScope(cmd, func(cmd *cobra.Command, args []string, scope configurationScope) error {
		variable := args[0]
		if !protocol.ValidEnvName(variable) {
			return fmt.Errorf("invalid variable name %q", variable)
		}
		request := protocol.VariableRequest{Session: scope.session(), Kind: kind, Variable: variable}
		var input any = request
		tool, verb := protocol.ToolDeleteValue, "Deleted"
		if value != nil {
			v, err := value(cmd, args)
			if err != nil {
				return err
			}
			if !protocol.ValidInputValue(v) {
				return errors.New("value must be single-line text without NUL characters")
			}
			input, tool, verb = protocol.SetValueRequest{VariableRequest: request, Value: v}, protocol.ToolSetValue, "Stored"
		}
		ctx := cmd.Context()
		manager, err := NewClient(ctx, config.Auth)
		if err != nil {
			return err
		}
		if err := manager.Call(ctx, ControlPlane(string(scope)), tool, input, &struct{}{}); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s in %s on %s. Deployments pick this up on their next deploy.\n",
			verb, kind, variable, scope.label(), manager.client.baseURL)
		return err
	})
}

// readSecret prompts without echo on a terminal and otherwise reads standard
// input, so scripts can pipe the value in.
func readSecret(cmd *cobra.Command, variable string) (string, error) {
	in := cmd.InOrStdin()
	var secret []byte
	var err error
	if file, ok := terminalFile(in); ok {
		stderr := cmd.ErrOrStderr()
		if _, err := fmt.Fprintf(stderr, "Paste %s: ", variable); err != nil {
			return "", err
		}
		secret, err = term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(stderr)
	} else {
		secret, err = clientutil.ReadLimited(in, maxRequestBody)
	}
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(secret), "\n")
	if value == "" {
		return "", errors.New("secret value is empty")
	}
	return value, nil
}

func scopedExample(title, command string) string {
	return fmt.Sprintf(`  # %s
  %s

  # Production, or one development session
  %s --env prod
  %s --session my-session`, title, command, command, command)
}
