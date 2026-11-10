package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

const targetHelp = "TARGET is an MCP server's URL, [ALIAS=]URL to send <ALIAS>_MCP_TOKEN as its bearer token, an MCP registry version URL (its streamable-http remote is used), or a dependency the server.json in the working directory declares under sh.holon.requires."

func newList(signIn typegen.SignIn) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "list TARGET", Short: "List an MCP server's tools, prompts and resources", Long: "List an MCP server's tools, prompts, resources and resource templates, after its name, version, negotiated protocol version and instructions.\n\n" + targetHelp, Example: `  # A remote server
  holon list https://huggingface.co/mcp

  # A dependency the server.json here declares
  holon list huggingface

  # A server that requires a token: DOCS_MCP_TOKEN is sent to it as a bearer token
  DOCS_MCP_TOKEN=... holon list docs=https://mcp.example.com/mcp

  # Everything the server reported, schemas included
  holon list huggingface --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		session, err := connect(cmd, args[0], signIn)
		if err != nil {
			return err
		}
		defer session.Close()
		catalog, err := listCatalog(cmd.Context(), session)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), catalog)
		}
		writeCatalog(cmd.OutOrStdout(), catalog)
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print everything the server reported as JSON")
	return cmd
}

func newCall(signIn typegen.SignIn) *cobra.Command {
	var asJSON bool
	var raw string
	cmd := &cobra.Command{Use: "call TARGET TOOL [KEY=VALUE...]", Short: "Call a tool on an MCP server", Long: "Call a tool on an MCP server and print its result: the structured content when the tool returns some, otherwise its text. A result the tool marks as an error exits 1.\n\n" +
		"Each KEY=VALUE is one argument. VALUE is read as JSON (5, true, [1,2], {\"a\":1}), or else as a string, so q=hello sends \"hello\"; quote a string that reads as JSON, as in id='\"123\"'. --args supplies a JSON object of arguments, which KEY=VALUE pairs override.\n\n" + targetHelp, Example: `  # Call a remote server's tool
  holon call https://huggingface.co/mcp hub_repo_search query=llama limit=3

  # A dependency the server.json here declares
  holon call huggingface hub_repo_search query=llama limit=3

  # Arguments as a JSON object
  holon call huggingface hub_repo_search --args '{"query":"llama","limit":3}'

  # The complete MCP result
  holon call huggingface hub_repo_search query=llama limit=3 --json`, Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		session, err := connect(cmd, args[0], signIn)
		if err != nil {
			return err
		}
		defer session.Close()
		// Listing first names the tools when TOOL is wrong and lets the SDK route the call by the tool's
		// definition.
		tools, err := collect(session.Tools(cmd.Context(), nil))
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(tools, func(tool *mcp.Tool) bool { return tool.Name == args[1] }) {
			names := make([]string, len(tools))
			for i, tool := range tools {
				names[i] = tool.Name
			}
			return fmt.Errorf("%s has no tool %q; it has %s", args[0], args[1], cmp.Or(strings.Join(names, ", "), "none"))
		}
		arguments, err := callArguments(args[2:], raw)
		if err != nil {
			return err
		}
		result, err := session.CallTool(cmd.Context(), &mcp.CallToolParams{Name: args[1], Arguments: arguments})
		if err != nil {
			return err
		}
		if asJSON {
			err = writeJSON(cmd.OutOrStdout(), result)
		} else {
			err = writeResult(cmd.OutOrStdout(), result)
		}
		if err == nil && result.IsError {
			err = fmt.Errorf("%s returned an error", args[1])
		}
		return err
	}}
	cmd.Flags().StringVar(&raw, "args", "", "Arguments as a JSON object")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the complete MCP result as JSON")
	return cmd
}

func connect(cmd *cobra.Command, arg string, signIn typegen.SignIn) (*mcp.ClientSession, error) {
	source, err := target(arg)
	if err != nil {
		return nil, err
	}
	return typegen.Connect(cmd.Context(), source, typegen.Options{Stderr: cmd.ErrOrStderr(), SignIn: signIn})
}

// target reads TARGET as a -s value. Only a running server can be listed or called, so a path is
// instead a dependency the server.json in the working directory declares.
func target(arg string) (typegen.Source, error) {
	source, err := typegen.ParseSource(arg)
	if err != nil || source.Path == "" {
		return source, err
	}
	declared, err := typegen.DeclaredSources(".", nil)
	if err != nil {
		return typegen.Source{}, err
	}
	names := make([]string, len(declared))
	for i, dependency := range declared {
		if dependency.Alias == arg {
			return dependency, nil
		}
		names[i] = dependency.Alias
	}
	return typegen.Source{}, fmt.Errorf("%s is not a URL or a dependency the server.json here declares (%s); for a server's source, pass the MCP URL it runs at, such as the one holon dev prints", arg, cmp.Or(strings.Join(names, ", "), "none"))
}

// catalog is what list prints: the initialize result, then every page of each listing the server
// advertises. A listing it does not advertise is empty.
type catalog struct {
	*mcp.InitializeResult
	Tools             []*mcp.Tool             `json:"tools"`
	Prompts           []*mcp.Prompt           `json:"prompts"`
	Resources         []*mcp.Resource         `json:"resources"`
	ResourceTemplates []*mcp.ResourceTemplate `json:"resourceTemplates"`
}

func listCatalog(ctx context.Context, session *mcp.ClientSession) (*catalog, error) {
	initialized := session.InitializeResult()
	c := &catalog{InitializeResult: initialized, Tools: []*mcp.Tool{}, Prompts: []*mcp.Prompt{}, Resources: []*mcp.Resource{}, ResourceTemplates: []*mcp.ResourceTemplate{}}
	capabilities := cmp.Or(initialized.Capabilities, &mcp.ServerCapabilities{})
	var group sync.WaitGroup
	errs := make([]error, 4)
	if capabilities.Tools != nil {
		group.Go(func() { c.Tools, errs[0] = collect(session.Tools(ctx, nil)) })
	}
	if capabilities.Prompts != nil {
		group.Go(func() { c.Prompts, errs[1] = collect(session.Prompts(ctx, nil)) })
	}
	if capabilities.Resources != nil {
		group.Go(func() { c.Resources, errs[2] = collect(session.Resources(ctx, nil)) })
		group.Go(func() { c.ResourceTemplates, errs[3] = collect(session.ResourceTemplates(ctx, nil)) })
	}
	group.Wait()
	return c, errors.Join(errs...)
}

func collect[T any](items iter.Seq2[T, error]) ([]T, error) {
	all := []T{}
	for item, err := range items {
		if err != nil {
			return nil, err
		}
		all = append(all, item)
	}
	return all, nil
}

func writeCatalog(w io.Writer, c *catalog) {
	info := cmp.Or(c.ServerInfo, &mcp.Implementation{})
	fmt.Fprintf(w, "%s %s (protocol %s)\n", cmp.Or(info.Name, "unnamed server"), cmp.Or(info.Version, "unversioned"), c.ProtocolVersion)
	if c.Instructions != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(c.Instructions))
	}
	writeSection(w, "Tools", c.Tools, func(tool *mcp.Tool) (string, string) {
		return tool.Name + "(" + signature(tool.InputSchema) + ")", tool.Description
	})
	writeSection(w, "Prompts", c.Prompts, func(prompt *mcp.Prompt) (string, string) {
		arguments := make([]string, len(prompt.Arguments))
		for i, argument := range prompt.Arguments {
			arguments[i] = argument.Name
			if !argument.Required {
				arguments[i] += "?"
			}
		}
		return prompt.Name + "(" + strings.Join(arguments, ", ") + ")", prompt.Description
	})
	writeSection(w, "Resources", c.Resources, func(resource *mcp.Resource) (string, string) {
		return resource.URI + "  " + resource.Name, resource.Description
	})
	writeSection(w, "Resource templates", c.ResourceTemplates, func(template *mcp.ResourceTemplate) (string, string) {
		return template.URITemplate + "  " + template.Name, template.Description
	})
}

// writeSection prints a listing's items, each with its description's first line. The JSON output
// keeps empty listings; here they are left out.
func writeSection[T any](w io.Writer, title string, items []T, line func(T) (head, description string)) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s (%d)\n", title, len(items))
	for _, item := range items {
		head, description := line(item)
		fmt.Fprintf(w, "  %s\n", head)
		if first, _, _ := strings.Cut(strings.TrimSpace(description), "\n"); first != "" {
			fmt.Fprintf(w, "      %s\n", first)
		}
	}
}

// signature lists a tool's parameters, the required ones first and the others marked ?. --json prints
// their schemas.
func signature(schema any) string {
	object, _ := schema.(map[string]any)
	properties, _ := object["properties"].(map[string]any)
	required, _ := object["required"].([]any)
	var first, rest []string
	for _, name := range slices.Sorted(maps.Keys(properties)) {
		if slices.Contains(required, any(name)) {
			first = append(first, name)
		} else {
			rest = append(rest, name+"?")
		}
	}
	return strings.Join(append(first, rest...), ", ")
}

// callArguments builds a tool call's arguments from --args, then each KEY=VALUE; a later KEY wins.
func callArguments(pairs []string, raw string) (map[string]any, error) {
	arguments := map[string]any{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &arguments); err != nil || arguments == nil {
			return nil, errors.New("--args must be a JSON object")
		}
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("%s: arguments are KEY=VALUE", pair)
		}
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) != nil {
			decoded = value // A VALUE that is not JSON is a string.
		}
		arguments[key] = decoded
	}
	return arguments, nil
}

// writeResult prints a tool result's structured content, or else its text blocks as they are and any
// other block as JSON.
func writeResult(w io.Writer, result *mcp.CallToolResult) error {
	if result.StructuredContent != nil {
		return writeJSON(w, result.StructuredContent)
	}
	for _, content := range result.Content {
		var err error
		if text, ok := content.(*mcp.TextContent); ok {
			_, err = fmt.Fprintln(w, strings.TrimSuffix(text.Text, "\n"))
		} else {
			err = writeJSON(w, content)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
