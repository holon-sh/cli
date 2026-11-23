package typegen

import (
	"cmp"
	"encoding/json"
	"fmt"
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

func DecodeTool(listed json.RawMessage) (*mcp.Tool, error) {
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
	fields.Tool.InputSchema = fields.InputSchema
	if fields.OutputSchema != nil {
		fields.Tool.OutputSchema = fields.OutputSchema
	}
	return &fields.Tool, nil
}

// Check finds the tool rules that name a tool s does not list, and the tools whose [MCP ToolAnnotations] declare
// neither readOnlyHint nor destructiveHint.
//
// [MCP ToolAnnotations]: https://modelcontextprotocol.io/specification/2026-07-28/schema#toolannotations
func (s *Served) Check(rules map[string]protocol.AccessRule) ([]Finding, error) {
	pages, err := s.pages("tools/list")
	if err != nil {
		return nil, err
	}
	listed, findings := map[string]bool{}, []Finding{}
	for _, data := range pages {
		var page struct{ Tools []json.RawMessage }
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, raw := range page.Tools {
			tool, err := DecodeTool(raw)
			if err != nil {
				return nil, err
			}
			if listed[tool.Name] {
				return nil, fmt.Errorf("tool %s is listed twice", tool.Name)
			}
			listed[tool.Name] = true
			if annotations := cmp.Or(tool.Annotations, &mcp.ToolAnnotations{}); !annotations.ReadOnlyHint && annotations.DestructiveHint == nil {
				findings = append(findings, Finding{RuleUndeclaredEffect, tool.Name})
			}
		}
	}
	for name := range rules {
		if !listed[name] {
			findings = append(findings, Finding{RuleUnlistedTool, name})
		}
	}
	slices.SortFunc(findings, func(a, b Finding) int { return cmp.Or(cmp.Compare(a.Tool, b.Tool), cmp.Compare(a.Rule, b.Rule)) })
	return findings, nil
}
