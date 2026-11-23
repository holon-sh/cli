package typegen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Finding struct {
	Rule string `json:"rule"`
	Tool string `json:"tool"`
}

const (
	RuleUnlistedTool     = "unlisted-tool"
	RuleUndeclaredEffect = "undeclared-effect"
)

type ListedTools map[string]bool

func (l ListedTools) Decode(listed json.RawMessage) (*mcp.Tool, error) {
	var fields struct {
		mcp.Tool

		InputSchema  json.RawMessage `json:"inputSchema"`
		OutputSchema json.RawMessage `json:"outputSchema"`
	}
	if err := json.Unmarshal(listed, &fields); err != nil {
		return nil, err
	}
	if err := mcptype.CheckTool(mcpcontract.Tool{Name: fields.Name, Input: fields.InputSchema, Output: fields.OutputSchema}); err != nil {
		return nil, err
	}
	if l[fields.Name] {
		return nil, fmt.Errorf("tool %s is listed twice", fields.Name)
	}
	l[fields.Name] = true
	fields.Tool.InputSchema = fields.InputSchema
	if fields.OutputSchema != nil {
		fields.Tool.OutputSchema = fields.OutputSchema
	}
	return &fields.Tool, nil
}

// Check finds the tools whose [MCP ToolAnnotations] declare neither readOnlyHint nor destructiveHint, and the tool
// rules that name a tool s does not list.
//
// [MCP ToolAnnotations]: https://modelcontextprotocol.io/specification/2026-07-28/schema#toolannotations
func (s *Served) Check(rules map[string]protocol.AccessRule) ([]Finding, error) {
	pages, err := s.pages("tools/list")
	if err != nil {
		return nil, err
	}
	listed, findings := ListedTools{}, []Finding{}
	for _, data := range pages {
		var page struct{ Tools []json.RawMessage }
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, raw := range page.Tools {
			tool, err := listed.Decode(raw)
			if err != nil {
				return nil, err
			}
			if annotations := cmp.Or(tool.Annotations, &mcp.ToolAnnotations{}); !annotations.ReadOnlyHint && annotations.DestructiveHint == nil {
				findings = append(findings, Finding{RuleUndeclaredEffect, tool.Name})
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(rules)) {
		if !listed[name] {
			findings = append(findings, Finding{RuleUnlistedTool, name})
		}
	}
	return findings, nil
}
