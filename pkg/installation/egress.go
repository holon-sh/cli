package installation

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/holon-sh/protocol"
)

const (
	toolGetEgress = "get_egress"
	toolSetEgress = "set_egress"
	egressAllApps = "*"
)

type egressRequest struct {
	App string `json:"app"`
}

type egressPolicy struct {
	Allow *protocol.Egress `json:"allow,omitempty"`
}

type setEgressRequest struct {
	egressRequest
	protocol.WritePrecondition

	Policy egressPolicy `json:"policy"`
}

type egressStatus struct {
	Reconciled      bool   `json:"reconciled"`
	RolloutComplete bool   `json:"rolloutComplete"`
	Degraded        bool   `json:"degraded"`
	Message         string `json:"message,omitempty"`
}

type egressResponse struct {
	egressPolicy

	Revision string       `json:"revision"`
	App      string       `json:"app"`
	Status   egressStatus `json:"status"`
}

func newEgressCommand(config *commandConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "egress",
		Short: "Manage the installation's egress ceiling",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "get [APP]",
			Short: "Show the egress ceiling of an app, or of every app",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runEgressGet(cmd, config, args)
			},
		},
		&cobra.Command{
			Use:   "set APP|'*' [HOST:PORT...]",
			Short: "Limit an app's egress to the destinations, or to none",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				allow, err := parseEgressDestinations(args[1:])
				if err != nil {
					return err
				}
				return runEgressSet(cmd, config, args[0], &allow)
			},
		},
		&cobra.Command{
			Use:   "delete APP|'*'",
			Short: "Remove an app's egress ceiling",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runEgressSet(cmd, config, args[0], nil)
			},
		},
	)
	return cmd
}

func validEgressApp(app string) error {
	if app != egressAllApps && !protocol.ValidAppName(app) {
		return fmt.Errorf("invalid app name %q", app)
	}
	return nil
}

func runEgressGet(cmd *cobra.Command, config *commandConfig, args []string) error {
	ctx := cmd.Context()
	apps := args
	if len(apps) == 1 {
		if err := validEgressApp(apps[0]); err != nil {
			return err
		}
	}
	client, err := NewClient(ctx, config.Auth)
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		session, err := client.Session(ctx, protocol.ProductionName)
		if err != nil {
			return err
		}
		apps = []string{egressAllApps}
		for _, app := range session.Apps {
			apps = append(apps, app.Name)
		}
	}
	results := make([]egressResponse, len(apps))
	for i, app := range apps {
		if err := client.Call(ctx, protocol.AdminPlane, toolGetEgress, egressRequest{App: app}, &results[i]); err != nil {
			return err
		}
	}
	return writeEgress(cmd.OutOrStdout(), results)
}

func runEgressSet(cmd *cobra.Command, config *commandConfig, app string, allow *protocol.Egress) error {
	if err := validEgressApp(app); err != nil {
		return err
	}
	ctx := cmd.Context()
	client, err := NewClient(ctx, config.Auth)
	if err != nil {
		return err
	}
	var current egressResponse
	if err := client.Call(ctx, protocol.AdminPlane, toolGetEgress, egressRequest{App: app}, &current); err != nil {
		return err
	}
	input := setEgressRequest{
		egressRequest:     egressRequest{App: app},
		WritePrecondition: protocol.WritePrecondition{ExpectedRevision: current.Revision, CreateOnly: current.Revision == ""},
		Policy:            egressPolicy{Allow: allow},
	}
	var result egressResponse
	if err := client.Call(ctx, protocol.AdminPlane, toolSetEgress, input, &result); err != nil {
		return err
	}
	return writeEgress(cmd.OutOrStdout(), []egressResponse{result})
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

func writeEgress(output io.Writer, results []egressResponse) error {
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "APP\tCEILING\tSTATUS")
	for _, result := range results {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", result.App, result.ceiling(), result.Status.summary())
	}
	return table.Flush()
}

func (result egressResponse) ceiling() string {
	switch {
	case result.Allow == nil:
		return "none set"
	case len(*result.Allow) == 0:
		return "no egress"
	}
	var destinations []string
	for _, destination := range *result.Allow {
		for _, port := range destination.Ports {
			destinations = append(destinations, net.JoinHostPort(destination.Host, strconv.Itoa(int(port))))
		}
	}
	return strings.Join(destinations, " ")
}

func (status egressStatus) summary() string {
	switch {
	case status.Degraded:
		return "degraded: " + status.Message
	case status.Reconciled && status.RolloutComplete:
		return "applied"
	}
	return "applying"
}
