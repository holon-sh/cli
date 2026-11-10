package installation

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/holon-sh/protocol"
)

const (
	toolListSessions          = "list_sessions"
	conditionEgressRestricted = "EgressRestricted"
	everyDevelopmentSession   = "Without --env or --session, it acts on the app in every development session that has it."
)

func newAppCommand(config *commandConfig) *cobra.Command {
	return commandGroup("app", "Inspect and remove deployed apps",
		withScope(&cobra.Command{Use: "list", Short: "List sessions and their apps with expiry, URL, source, access and egress", Args: cobra.NoArgs,
			Long: "List every development session and its apps. A session whose apps were all removed stays listed until it expires.",
			Example: `  # List every development session and its apps
  holon app list

  # Production, or one development session
  holon app list --env prod
  holon app list --session my-session`},
			func(cmd *cobra.Command, _ []string, scope configurationScope) error {
				manager, err := NewClient(cmd.Context(), config.Auth)
				if err != nil {
					return err
				}
				sessions, err := scopeSessions(cmd.Context(), manager, scope)
				if err != nil {
					return err
				}
				now := config.clock().Now()
				table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(table, "SESSION\tEXPIRES\tAPP\tURL\tSOURCE\tACCESS\tEGRESS\tRESTRICTED")
				for _, session := range sessions {
					expires := expiresIn(session.ExpiresAt, now)
					if len(session.Apps) == 0 {
						_, _ = fmt.Fprintf(table, "%s\t%s\t-\n", session.Name, expires)
					}
					for _, app := range session.Apps {
						_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", session.Name, expires, app.Name, appURL(session, app.Name),
							appSource(app), accessSummary(app.Access), egressSummary(app.Egress), egressCut(session, app.Name))
					}
				}
				return table.Flush()
			}),
		withScope(&cobra.Command{Use: "get APP", Short: "Print an app's declaration as YAML", Args: cobra.ExactArgs(1),
			Long: "Print an app's declaration. " + everyDevelopmentSession,
			Example: `  # Print an app's declaration from every development session
  holon app get chat

  # Production, or one development session
  holon app get chat --env prod
  holon app get chat --session my-session`},
			func(cmd *cobra.Command, args []string, scope configurationScope) error {
				_, targets, err := sessionApps(cmd, config, scope, args[0])
				if err != nil {
					return err
				}
				for _, target := range targets {
					data, err := yaml.Marshal(struct {
						protocol.App

						EgressRestricted string `json:"egressRestricted,omitempty"`
					}{target.app, egressCut(target.session, args[0])})
					if err != nil {
						return err
					}
					if len(targets) > 1 {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "---\n# %s\n", target.label())
					}
					if _, err := cmd.OutOrStdout().Write(data); err != nil {
						return err
					}
				}
				return nil
			}),
		withScope(&cobra.Command{Use: "remove APP", Short: "Remove an app from a deployment", Args: cobra.ExactArgs(1),
			Long: "Remove an app. " + everyDevelopmentSession,
			Example: `  # Remove an app from every development session
  holon app remove chat

  # Remove an app from production
  holon app remove chat --env prod

  # Remove an app from one development session
  holon app remove chat --session my-session`},
			func(cmd *cobra.Command, args []string, scope configurationScope) error {
				manager, targets, err := sessionApps(cmd, config, scope, args[0])
				if err != nil {
					return err
				}
				for _, target := range targets {
					request := protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]{Remove: args}
					request.Session, request.ExpectedRevision = target.session.Name, target.session.Revision
					request.Desired.Apps = []protocol.App{}
					if err := manager.Call(cmd.Context(), ControlPlane(target.session.Name), protocol.ToolUpdateSession, request, nil); err != nil {
						return err
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s.\n", args[0], target.label())
				}
				return nil
			}),
	)
}

func newAccessCommand(config *commandConfig) *cobra.Command {
	return commandGroup("access", "Set the access rules of deployed apps",
		withScope(&cobra.Command{
			Use:   "set APP[/TOOL] public|oauth [SCOPE...]",
			Short: "Replace an app's server rule, or one tool's rule",
			Long:  "Replace the server rule of APP, or the rule of APP/TOOL. A tool rule replaces the server rule for that tool. " + everyDevelopmentSession,
			Args:  cobra.MinimumNArgs(2),
			Example: `  # Require sign-in for an app in every development session, with or without scopes
  holon access set chat oauth
  holon access set chat oauth app:chat:read

  # Let anyone call an app in production; a development session admits only signed-in developers whatever the rule
  holon access set chat public --env prod

  # Replace the rule of one tool
  holon access set chat/send_message oauth app:chat:write

  # Production, or one development session
  holon access set chat oauth --env prod
  holon access set chat oauth --session my-session`,
		}, func(cmd *cobra.Command, args []string, scope configurationScope) error {
			rule := protocol.AccessRule{}
			switch {
			case args[1] == "oauth":
				rule.OAuth = &protocol.OAuthRequirement{Scopes: args[2:]}
			case args[1] != "public":
				return errors.New("access must be public or oauth")
			case len(args) > 2:
				return errors.New("public access takes no scopes")
			}
			return updateAccess(cmd, config, scope, args[0], func(access *protocol.AppAccess, tool string) error {
				if tool == "" {
					access.Server = &rule
					return nil
				}
				if access.Tools == nil {
					access.Tools = map[string]protocol.AccessRule{}
				}
				access.Tools[tool] = rule
				return nil
			})
		}),
		withScope(&cobra.Command{
			Use:   "delete APP/TOOL",
			Short: "Delete a tool rule so the tool follows the server rule",
			Args:  cobra.ExactArgs(1),
			Example: `  # Make a tool follow its app's rule again in every development session
  holon access delete chat/send_message

  # Production, or one development session
  holon access delete chat/send_message --env prod
  holon access delete chat/send_message --session my-session`,
		}, func(cmd *cobra.Command, args []string, scope configurationScope) error {
			return updateAccess(cmd, config, scope, args[0], func(access *protocol.AppAccess, tool string) error {
				if tool == "" {
					return errors.New("the server rule cannot be deleted; replace it with holon access set")
				}
				if _, ok := access.Tools[tool]; !ok {
					return fmt.Errorf("%s has no rule", args[0])
				}
				delete(access.Tools, tool)
				return nil
			})
		}),
	)
}

func updateAccess(
	cmd *cobra.Command, config *commandConfig, scope configurationScope, target string,
	change func(access *protocol.AppAccess, tool string) error,
) error {
	name, tool, _ := strings.Cut(target, "/")
	manager, targets, err := sessionApps(cmd, config, scope, name)
	if err != nil {
		return err
	}
	for i, target := range targets {
		app := protocol.WritableApps([]protocol.App{target.app})[0]
		access := *app.Access
		access.Tools = maps.Clone(access.Tools)
		app.Access = &access
		if err := change(app.Access, tool); err != nil {
			return fmt.Errorf("%s: %w", target.label(), err)
		}
		targets[i].app = app
	}
	for _, target := range targets {
		if _, err := replaceExecution(cmd.Context(), manager, target.session.Name, target.session,
			protocol.AppsRequestFor[protocol.App]{Apps: []protocol.App{target.app}}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s access in %s: %s\n", name, target.label(), accessSummary(target.app.Access))
	}
	return nil
}

type sessionTarget struct {
	session protocol.SessionResponse
	app     protocol.App
}

func (target sessionTarget) label() string {
	return configurationScope(target.session.Name).label()
}

func scopeSessions(ctx context.Context, manager *Client, scope configurationScope) ([]protocol.SessionResponse, error) {
	if scope != "" {
		session, err := manager.Session(ctx, string(scope))
		return []protocol.SessionResponse{session}, err
	}
	var list struct {
		Items []protocol.SessionResponse `json:"items"`
	}
	err := manager.Call(ctx, protocol.DevPlane, toolListSessions, struct{}{}, &list)
	return list.Items, err
}

func sessionApps(cmd *cobra.Command, config *commandConfig, scope configurationScope, name string) (*Client, []sessionTarget, error) {
	if !protocol.ValidAppName(name) {
		return nil, nil, fmt.Errorf("invalid app name %q", name)
	}
	manager, err := NewClient(cmd.Context(), config.Auth)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := scopeSessions(cmd.Context(), manager, scope)
	if err != nil {
		return nil, nil, err
	}
	var targets []sessionTarget
	for _, session := range sessions {
		if index := slices.IndexFunc(session.Apps, func(app protocol.App) bool { return app.Name == name }); index >= 0 {
			targets = append(targets, sessionTarget{session, session.Apps[index]})
		}
	}
	if len(targets) == 0 {
		where := "any development session"
		if scope != "" {
			where = scope.label()
		}
		return nil, nil, fmt.Errorf("app %s is not in %s", name, where)
	}
	return manager, targets, nil
}

// expiresIn is how long a development session has left, at least a minute; production never expires.
func expiresIn(expiresAt string, now time.Time) string {
	expiry, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return "-"
	}
	return strings.TrimSuffix(max(expiry.Sub(now), time.Minute).Round(time.Minute).String(), "0s")
}

// appURL is the app's MCP endpoint once the session serves it.
func appURL(session protocol.SessionResponse, app string) string {
	for _, endpoint := range session.Endpoints {
		if endpoint.Name == app {
			return endpoint.URL
		}
	}
	return "-"
}

func appSource(app protocol.App) string {
	switch {
	case app.Remote != nil:
		host, _, _ := strings.Cut(strings.TrimPrefix(app.Remote.URL, "https://"), "/")
		return "remote " + host
	case app.Release != nil && app.Release.Name != "":
		return "registry " + app.Release.Name + "@" + app.Release.Version
	}
	repository, digest, _ := strings.Cut(app.Image, "@sha256:")
	return "image " + repository + "@sha256:" + digest[:12]
}

func accessSummary(access *protocol.AppAccess) string {
	summary := accessRule(*access.Server)
	for _, tool := range slices.Sorted(maps.Keys(access.Tools)) {
		summary += ", " + tool + ": " + accessRule(access.Tools[tool])
	}
	return summary
}

func accessRule(rule protocol.AccessRule) string {
	if rule.OAuth == nil {
		return "public"
	}
	return strings.Join(append([]string{"oauth"}, rule.OAuth.Scopes...), " ")
}

func egressSummary(egress protocol.Egress) string {
	if egress == nil {
		return "any"
	}
	if len(egress) == 0 {
		return "none"
	}
	destinations := make([]string, 0, len(egress))
	for _, destination := range egress {
		ports := make([]string, 0, len(destination.Ports))
		for _, port := range destination.Ports {
			ports = append(ports, strconv.Itoa(int(port)))
		}
		destinations = append(destinations, destination.Host+":"+strings.Join(ports, ","))
	}
	return strings.Join(destinations, " ")
}

func egressCut(session protocol.SessionResponse, app string) string {
	condition := session.Condition(conditionEgressRestricted)
	if condition.Status != "True" {
		return ""
	}
	_, cuts, _ := strings.Cut(condition.Message, " cuts ")
	for cut := range strings.SplitSeq(cuts, "; ") {
		if hosts, ok := strings.CutPrefix(cut, app+": "); ok {
			return hosts
		}
	}
	return ""
}
