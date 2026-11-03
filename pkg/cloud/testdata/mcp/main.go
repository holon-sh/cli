package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "transport-fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Value string `json:"value"`
	},
	) (*mcp.CallToolResult, any, error) {
		directory, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		_, _ = fmt.Fprintln(os.Stderr, "fixture echo")
		return nil, map[string]any{
			"value": args.Value, "directory": directory, "uid": os.Getuid(), "environment": os.Getenv("FIXTURE_VALUE"),
			"arguments": os.Args[1:], "injected": os.Getenv("INJECTED_VALUE"), "token": os.Getenv("TOKEN"), "empty": os.Getenv("EMPTY"),
		}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "crash"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		os.Exit(23)
		return nil, nil, nil
	})
	_, _ = fmt.Fprintln(os.Stderr, "fixture started")
	var err error
	if len(os.Args) > 1 && os.Args[1] == "http" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
		hosting := &http.Server{Addr: ":3000", Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		err = hosting.ListenAndServe()
	} else {
		// Like most stdio servers, which predate MCP 2026-07-28, reject server/discover as an unknown method.
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method == "server/discover" {
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}
				}
				return next(ctx, method, req)
			}
		})
		err = server.Run(context.Background(), &mcp.StdioTransport{})
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
