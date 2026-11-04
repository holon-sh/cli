package installation

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/holon-sh/protocol"
)

const (
	toolListSessions          = "list_sessions"
	conditionEgressRestricted = "EgressRestricted"
)

type removeAppsRequest struct {
	protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]

	Remove []string `json:"remove"`
}

func newAppCommand(config *commandConfig) *cobra.Command {
	return commandGroup("app", "Inspect and remove deployed apps",
		withScope(&cobra.Command{Use: "list", Short: "List apps with their source, access and egress", Args: cobra.NoArgs},
			func(cmd *cobra.Command, _ []string, scope configurationScope) error {
				manager, err := NewClient(cmd.Context(), config.Auth)
				if err != nil {
					return err
				}
				var sessions []protocol.SessionResponse
				if scope == "" {
					var list struct {
						Items []protocol.SessionResponse `json:"items"`
					}
					err = manager.Call(cmd.Context(), protocol.DevPlane, toolListSessions, struct{}{}, &list)
					sessions = list.Items
				} else {
					var session protocol.SessionResponse
					session, err = manager.Session(cmd.Context(), string(scope))
					sessions = []protocol.SessionResponse{session}
				}
				if err != nil {
					return err
				}
				table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(table, "SESSION\tAPP\tSOURCE\tACCESS\tEGRESS\tRESTRICTED")
				for _, session := range sessions {
					for _, app := range session.Apps {
						_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", session.Name, app.Name, appSource(app),
							accessSummary(app.Access), egressSummary(app.Egress), egressCut(session, app.Name))
					}
				}
				return table.Flush()
			}),
		withScope(&cobra.Command{Use: "get APP", Short: "Print an app's declaration as YAML", Args: cobra.ExactArgs(1)},
			func(cmd *cobra.Command, args []string, scope configurationScope) error {
				_, session, index, err := sessionApp(cmd, config, scope, args[0])
				if err != nil {
					return err
				}
				data, err := yaml.Marshal(struct {
					protocol.App

					EgressRestricted string `json:"egressRestricted,omitempty"`
				}{session.Apps[index], egressCut(session, args[0])})
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}),
		withScope(&cobra.Command{Use: "remove APP", Short: "Remove an app from a deployment", Args: cobra.ExactArgs(1)},
			func(cmd *cobra.Command, args []string, scope configurationScope) error {
				manager, session, _, err := sessionApp(cmd, config, scope, args[0])
				if err != nil {
					return err
				}
				request := removeAppsRequest{Remove: args}
				request.Session, request.ExpectedRevision = session.Name, session.Revision
				request.Desired.Apps = []protocol.App{}
				if err := manager.Call(cmd.Context(), ControlPlane(session.Name), protocol.ToolUpdateSession, request, nil); err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s.\n", args[0], scope.label())
				return err
			}),
	)
}

func newAccessCommand(config *commandConfig) *cobra.Command {
	return commandGroup("access", "Set the access rules of deployed apps",
		withScope(&cobra.Command{
			Use:   "set APP[/TOOL] public|oauth [SCOPE...]",
			Short: "Replace an app's server rule, or one tool's rule",
			Long:  "Replace the server rule of APP, or the rule of APP/TOOL. A tool rule replaces the server rule for that tool.",
			Args:  cobra.MinimumNArgs(2),
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
	manager, session, index, err := sessionApp(cmd, config, scope, name)
	if err != nil {
		return err
	}
	app := protocol.WritableApps(session.Apps[index : index+1])[0]
	access := *app.Access
	access.Tools = maps.Clone(access.Tools)
	app.Access = &access
	if err := change(app.Access, tool); err != nil {
		return err
	}
	if _, err := replaceExecution(cmd.Context(), manager, session.Name, session,
		protocol.AppsRequestFor[protocol.App]{Apps: []protocol.App{app}}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s access in %s: %s\n", name, scope.label(), accessSummary(app.Access))
	return err
}

func sessionApp(cmd *cobra.Command, config *commandConfig, scope configurationScope, name string) (*Client, protocol.SessionResponse, int, error) {
	if scope == "" {
		return nil, protocol.SessionResponse{}, 0, errors.New("name a development session with --session NAME, or production with --env prod")
	}
	if !protocol.ValidAppName(name) {
		return nil, protocol.SessionResponse{}, 0, fmt.Errorf("invalid app name %q", name)
	}
	manager, err := NewClient(cmd.Context(), config.Auth)
	if err != nil {
		return nil, protocol.SessionResponse{}, 0, err
	}
	session, err := manager.Session(cmd.Context(), string(scope))
	if err != nil {
		return nil, protocol.SessionResponse{}, 0, err
	}
	index := slices.IndexFunc(session.Apps, func(app protocol.App) bool { return app.Name == name })
	if index < 0 {
		return nil, protocol.SessionResponse{}, 0, fmt.Errorf("app %s is not in %s", name, scope.label())
	}
	return manager, session, index, nil
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
