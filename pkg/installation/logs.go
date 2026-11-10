package installation

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/holon-sh/protocol"
)

const toolGetLogs = "get_logs"

func newLogsCommand(config *commandConfig) *cobra.Command {
	var since string
	var follow bool
	cmd := withScope(&cobra.Command{
		Use:   "logs [APP]",
		Short: "Show what apps write to stdout and stderr, and each MCP call",
		Long: "Show what an app, or every app, writes to stdout and stderr, with Holon's record of each MCP call. " +
			"Without --env or --session, it reads the development session that runs the app.",
		Example: `  # The development session's logs
  holon logs chat

  # Production, from the last hour on, then as they arrive
  holon logs chat --env prod --since 1h -f

  # Every app in one development session
  holon logs --session my-session`,
		Args: cobra.MaximumNArgs(1)},
		func(cmd *cobra.Command, args []string, scope configurationScope) error {
			manager, err := NewClient(cmd.Context(), config.Auth)
			if err != nil {
				return err
			}
			input := protocol.SessionLogsRequest{Since: since}
			input.Session = string(scope)
			if len(args) > 0 {
				input.App = args[0]
			}
			if scope == "" {
				if input.Session, err = logSession(cmd, manager, input.App); err != nil {
					return err
				}
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
				if at, err := time.Parse(time.RFC3339Nano, r.Time); err == nil {
					r.Time = logTime(at)
				}
				lines.WriteString(r.Time + " " + r.Severity + " " + r.App + " " + r.Body)
				if r.TraceID != "" {
					lines.WriteString(" trace_id=" + r.TraceID)
				}
				lines.WriteByte('\n')
			}
			_, err = io.WriteString(cmd.OutOrStdout(), lines.String())
			return err
		})
	cmd.Flags().StringVar(&since, "since", "", "Start this duration ago or at this RFC 3339 time (default 15m)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep showing logs as they arrive")
	return cmd
}

func logSession(cmd *cobra.Command, manager *Client, app string) (string, error) {
	sessions, err := scopeSessions(cmd.Context(), manager, "")
	if err != nil {
		return "", err
	}
	var names []string
	for _, session := range sessions {
		if app == "" || slices.ContainsFunc(session.Apps, func(candidate protocol.App) bool { return candidate.Name == app }) {
			names = append(names, session.Name)
		}
	}
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		if app == "" {
			return "", errors.New("no development session is running; use --env prod for production")
		}
		return "", fmt.Errorf("app %s is not in any development session; use --env prod for production", app)
	}
	return "", fmt.Errorf("choose a development session with --session: %s", strings.Join(names, ", "))
}
