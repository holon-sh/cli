package installation

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	return newValuesCommand(config, "secret", "API_KEY", &cobra.Command{
		Use:  "set VARIABLE",
		Long: "Set a secret value. On a terminal the value is read from a hidden prompt; otherwise it is read from standard input, with the trailing newline removed.",
		Example: scopedExample("Set the secret API_KEY for development", "holon secret set API_KEY") + `

  # Pipe the value in from a script or CI
  printenv API_KEY | holon secret set API_KEY`,
		Args: cobra.ExactArgs(1),
	}, func(cmd *cobra.Command, args []string) (string, error) {
		return readSecret(cmd, args[0])
	})
}

func newConfigCommand(config *commandConfig) *cobra.Command {
	return newValuesCommand(config, "config", "REGION", &cobra.Command{
		Use:     "set VARIABLE VALUE",
		Example: scopedExample("Set the config REGION for development", "holon config set REGION us-east1"),
		Args:    cobra.ExactArgs(2),
	}, func(_ *cobra.Command, args []string) (string, error) {
		return args[1], nil
	})
}

// newValuesCommand groups set, which stores what value returns, with delete for one kind of value.
func newValuesCommand(config *commandConfig, kind, sample string, set *cobra.Command, value func(*cobra.Command, []string) (string, error)) *cobra.Command {
	set.Short = "set a " + kind + " value"
	return commandGroup(kind, "Manage "+kind+" values",
		newValueCommand(kind, set, value, config),
		newValueCommand(kind, &cobra.Command{
			Use:     "delete VARIABLE",
			Short:   "delete a " + kind + " value",
			Example: scopedExample("Delete the "+kind+" "+sample+" for development", "holon "+kind+" delete "+sample),
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
		session := string(scope)
		if scope == protocol.ProductionName {
			session = ""
		}
		request := protocol.VariableRequest{Session: session, Kind: kind, Variable: variable}
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

// declareSetValues declares every config value and secret set on the plane that serves session name as an input of
// each app built from source, so the platform puts it in the app's environment under its own name. The platform picks
// the value a session sees, and a name set only for another session stays unset; a name listed twice keeps its first
// kind. Names the app already declares or templates, Holon's PORT, HOLON_ and dependency URL variables, and names
// another app declares as the other kind are left alone.
func declareSetValues(ctx context.Context, manager *Client, name string, manifest manifestSpec, apps []protocol.App) error {
	var listed protocol.ConfigValues
	if err := manager.Call(ctx, ControlPlane(name), protocol.ToolListValues, struct{}{}, &listed); err != nil {
		return fmt.Errorf("list config values: %w", err)
	}
	kinds, err := inputKinds(apps)
	if err != nil {
		return err
	}
	for i, source := range manifest.Apps {
		app := &apps[i]
		if source.Path == "" {
			continue
		}
		env := map[string]protocol.Input{}
		maps.Copy(env, app.Env)
		for _, value := range listed.Values {
			variable, input := value.Variable, protocol.Input{IsSecret: value.Kind == "secret"}
			_, declared := env[variable]
			_, templated := app.EnvTemplates[variable]
			kind, used := kinds[variable]
			if declared || templated || used && kind != input.Kind() || variable == "PORT" || strings.HasPrefix(variable, "HOLON_") ||
				slices.ContainsFunc(app.Requires, func(target string) bool { return protocol.DependencyEnvName(target) == variable }) {
				continue
			}
			env[variable] = input
		}
		app.Env = env
	}
	return nil
}
