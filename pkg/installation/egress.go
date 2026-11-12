package installation

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/holon-sh/protocol"
)

const egressAllApps = "*"

func newEgressCommand(config *commandConfig) *cobra.Command {
	return commandGroup("egress", "Manage the installation's egress ceiling",
		&cobra.Command{
			Use:   "get APP|'*'",
			Short: "Show an app's egress ceiling, or the installation-wide one",
			Example: `  # An app's egress ceiling
  holon egress get chat

  # The installation-wide ceiling
  holon egress get '*'`,
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runEgressGet(cmd, config, args[0])
			},
		},
		&cobra.Command{
			Use:   "set APP|'*' [HOST:PORT...]",
			Short: "Limit an app's egress to the destinations, or to none",
			Example: `  # Limit an app to the destinations
  holon egress set chat api.example.com:443 db.example.com:5432

  # Allow no egress
  holon egress set chat

  # The installation-wide ceiling
  holon egress set '*' api.example.com:443`,
			Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				allow, err := parseEgressDestinations(args[1:])
				if err != nil {
					return err
				}
				return runEgressSet(cmd, config, args[0], allow)
			},
		},
		&cobra.Command{
			Use:   "delete APP|'*'",
			Short: "Remove an app's egress ceiling",
			Example: `  # Remove an app's ceiling
  holon egress delete chat

  # Remove the installation-wide ceiling
  holon egress delete '*'`,
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runEgressSet(cmd, config, args[0], nil)
			},
		},
	)
}

func validEgressApp(app string) error {
	if app != egressAllApps && !protocol.ValidAppName(app) {
		return fmt.Errorf("invalid app name %q", app)
	}
	return nil
}

func runEgressGet(cmd *cobra.Command, config *commandConfig, app string) error {
	if err := validEgressApp(app); err != nil {
		return err
	}
	ctx := cmd.Context()
	client, err := NewClient(ctx, config.Auth)
	if err != nil {
		return err
	}
	result, err := getEgress(ctx, client, app)
	if err != nil {
		return err
	}
	return writeEgress(cmd.OutOrStdout(), result)
}

func getEgress(ctx context.Context, client *Client, app string) (protocol.EgressResponse, error) {
	var result protocol.EgressResponse
	err := client.Call(ctx, protocol.AdminPlane, protocol.ToolGetEgress, protocol.EgressRequest{App: app}, &result)
	return result, err
}

func runEgressSet(cmd *cobra.Command, config *commandConfig, app string, allow protocol.Egress) error {
	if err := validEgressApp(app); err != nil {
		return err
	}
	ctx := cmd.Context()
	client, err := NewClient(ctx, config.Auth)
	if err != nil {
		return err
	}
	current, err := getEgress(ctx, client, app)
	if err != nil {
		return err
	}
	input := protocol.SetEgressRequest{
		EgressRequest:     protocol.EgressRequest{App: app},
		WritePrecondition: protocol.WritePrecondition{ExpectedRevision: current.Revision, CreateOnly: current.Revision == ""},
		Policy:            protocol.AppEgressPolicy{Allow: allow},
	}
	var result protocol.EgressResponse
	if err := client.Call(ctx, protocol.AdminPlane, protocol.ToolSetEgress, input, &result); err != nil {
		return err
	}
	return writeEgress(cmd.OutOrStdout(), result)
}

func parseEgressDestinations(destinations []string) (protocol.Egress, error) {
	allow := protocol.Egress{}
	for _, destination := range destinations {
		host, port, err := net.SplitHostPort(destination)
		if err != nil {
			return nil, err
		}
		number, err := strconv.ParseInt(port, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("port in %q must be an integer", destination)
		}
		allow = append(allow, protocol.EgressDestination{Host: host, Ports: []int32{int32(number)}})
	}
	return allow.Normalize()
}

func writeEgress(output io.Writer, result protocol.EgressResponse) error {
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "APP\tCEILING\tSTATUS")
	_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", result.App, ceilingSummary(result.Allow), statusSummary(result.Status))
	return table.Flush()
}

func ceilingSummary(allow protocol.Egress) string {
	switch {
	case allow == nil:
		return "none set"
	case len(allow) == 0:
		return "no egress"
	}
	var destinations []string
	for _, destination := range allow {
		for _, port := range destination.Ports {
			destinations = append(destinations, net.JoinHostPort(destination.Host, strconv.Itoa(int(port))))
		}
	}
	return strings.Join(destinations, " ")
}

func statusSummary(status protocol.EgressStatus) string {
	switch {
	case status.Degraded:
		return "degraded: " + status.Message
	case status.Reconciled && status.RolloutComplete:
		return "applied"
	}
	return "applying"
}
