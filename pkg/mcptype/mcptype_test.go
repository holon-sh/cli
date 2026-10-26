package mcptype

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestGenerate(t *testing.T) {
	request := GenerateRequest{
		Target: "go", Identity: mcpcontract.Identity{Name: "caller", Version: "1"}, GoModule: "example.com/runtime",
		Contracts: map[string]*mcpcontract.Contract{"orders": {Tools: []mcpcontract.Tool{{Name: "get", Input: json.RawMessage(`{"type":"object"}`)}}}},
		Runtime:   map[string][]byte{"blob": {0, 1}},
	}
	files, err := Generate(t.Context(), request)
	if err != nil || string(files["blob"]) != "\x00\x01" || !strings.Contains(string(files["clients.go"]), `holon "example.com/runtime"`) {
		t.Fatalf("generated package: %v %v", slices.Sorted(maps.Keys(files)), err)
	}
	files["blob"][0] = 2
	if len(request.Runtime) != 1 || string(request.Runtime["blob"]) != "\x00\x01" {
		t.Fatal("generation changed caller-owned runtime files")
	}
	request.Contracts["orders"].Tools[0].Input = json.RawMessage(`{"type":"string"}`)
	if _, err := Generate(t.Context(), request); err == nil || !strings.Contains(err.Error(), "inputSchema must have type object") {
		t.Fatalf("invalid contract was not rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Generate(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled generation: %v", err)
	}
}

func TestChanges(t *testing.T) {
	get := mcpcontract.Tool{Name: "get", Input: json.RawMessage(`{"type":"object"}`)}
	summary := mcpcontract.Resource{URI: "orders://summary", Output: json.RawMessage(`{"type":"object"}`)}
	expected := mcpcontract.Expectation{
		Identity: mcpcontract.Identity{Name: "example/orders", Version: "1"},
		Contract: mcpcontract.Contract{Tools: []mcpcontract.Tool{get}, Resources: []mcpcontract.Resource{summary}},
	}
	published := &mcpcontract.Metadata{
		Version: mcpcontract.Version, Identity: mcpcontract.Identity{Name: "example/orders", Version: "2"},
		Contract: &mcpcontract.Contract{Resources: []mcpcontract.Resource{{URI: summary.URI, Output: json.RawMessage(`{"type":"array"}`)}}},
	}
	changes, err := Changes(t.Context(), expected, published)
	if want := []Change{
		{Kind: "identity", Text: "identity example/orders@1 expected, example/orders@2 published"},
		{Kind: "tool_removed", Name: "get", Text: "tool get removed"},
		{Kind: "resource_output_changed", Name: "orders://summary", Text: "resource orders://summary contents changed"},
	}; err != nil || !slices.Equal(changes, want) {
		t.Fatalf("changes misreported: %v %v", changes, err)
	}
	if changes, err := Changes(t.Context(), expected, nil); err != nil || len(changes) != 1 || changes[0].Text != "no published contract" {
		t.Fatalf("missing contract accepted: %v %v", changes, err)
	}
	published.Version = 2
	if _, err := Changes(t.Context(), expected, published); err == nil {
		t.Fatal("invalid published metadata accepted")
	}
}

func TestCanonicalMetadata(t *testing.T) {
	metadata := mcpcontract.Metadata{Version: mcpcontract.Version, Identity: mcpcontract.Identity{Name: "example/orders", Version: "1"}}
	if form, err := Canonical(t.Context(), map[string]any{"metadata": metadata}); err != nil || form.Digest != "" {
		t.Fatalf("metadata without a contract: %+v %v", form, err)
	}
	metadata.Contract = &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "get", Input: json.RawMessage(`{"type":"object","description":"docs"}`)}}}
	form, err := Canonical(t.Context(), map[string]any{"metadata": metadata})
	if err != nil || string(form.Canonical) != `{"tools":[{"inputSchema":{"type":"object"},"name":"get"}]}` || !strings.HasPrefix(form.Digest, "sha256:") {
		t.Fatalf("metadata contract: %s %q %v", form.Canonical, form.Digest, err)
	}
	metadata.Contract.Tools[0].Input = json.RawMessage(`{"type":"string"}`)
	if _, err := Canonical(t.Context(), map[string]any{"metadata": metadata}); err == nil {
		t.Fatal("invalid published metadata accepted")
	}
}

func TestCanonicalLeavesInputUntouched(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal([]byte(`{"schema":{"type":"object","required":["b","a"],"properties":{"a":{"type":"string","description":"keep"}}}}`), &document); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(document)
	form, err := Canonical(t.Context(), document)
	if err != nil || string(form.Canonical) != `{"properties":{"a":{"type":"string"}},"required":["a","b"],"type":"object"}` {
		t.Fatalf("canonical schema: %s %v", form.Canonical, err)
	}
	after, _ := json.Marshal(document)
	if !bytes.Equal(before, after) {
		t.Fatalf("canonicalization changed the caller's document: %s", after)
	}
}
