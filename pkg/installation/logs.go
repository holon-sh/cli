package installation

import (
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/holon-sh/protocol"
)

const toolGetLogs = "get_logs"

func newLogsCommand(config *commandConfig) *cobra.Command {
	var input protocol.SessionLogsRequest
	var attempt int64
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs SESSION",
		Short: "Show what a session's apps write to stdout and stderr, and each MCP call",
		Example: `  # Production logs from the last hour
  holon logs production --since 1h

  # Stream one app's logs in a development session
  holon logs gentle-otter-482 --app chat -f`,
		Args: cobra.MatchAll(cobra.ExactArgs(1), func(_ *cobra.Command, args []string) error {
			if !protocol.ValidDNSLabel(args[0]) {
				return errors.New("session must be a valid name")
			}
			return nil
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := NewClient(cmd.Context(), config.Auth)
			if err != nil {
				return err
			}
			input.Session = args[0]
			if cmd.Flags().Changed("attempt") {
				input.Attempt = &attempt
			}
			if follow {
				return manager.StreamLogs(cmd.Context(), input, cmd.OutOrStdout())
			}
			var logs struct {
				Records []struct {
					Time, Severity, App, Body, TraceID string
				}
			}
			if err := manager.Call(cmd.Context(), ControlPlane(input.Session), toolGetLogs, input, &logs); err != nil {
				return err
			}
			var lines strings.Builder
			for _, r := range logs.Records {
				lines.WriteString(r.Time + " " + r.Severity + " " + r.App + " " + r.Body)
				if r.TraceID != "" {
					lines.WriteString(" trace_id=" + r.TraceID)
				}
				lines.WriteByte('\n')
			}
			_, err = io.WriteString(cmd.OutOrStdout(), lines.String())
			return err
		},
	}
	cmd.Flags().StringVar(&input.App, "app", "", "Show one app")
	cmd.Flags().StringVar(&input.Since, "since", "", "Start this duration ago or at this RFC 3339 time (default 15m)")
	cmd.Flags().StringVar(&input.TraceID, "trace", "", "Show one trace ID")
	cmd.Flags().StringVar(&input.Text, "text", "", "Show lines containing this text")
	cmd.Flags().StringVar(&input.Hook, "hook", "", "Show a lifecycle hook, such as 1.2.0/pre-upgrade")
	cmd.Flags().StringVar(&input.Operation, "operation", "", "Show a lifecycle operation")
	cmd.Flags().Int64Var(&attempt, "attempt", 0, "Show a lifecycle attempt")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream until interrupted")
	return cmd
}
