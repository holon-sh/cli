package mcptype

/*
#cgo pkg-config: mcptype
#include <mcptype.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// Change is one way a published contract breaks an expectation. Name is the tool name or resource URI, if any.
type Change struct {
	Kind string
	Name string
	Text string
}

// CanonicalForm is a document with annotations removed and unordered keywords sorted.
// Both fields are empty for metadata without a contract.
type CanonicalForm struct {
	Canonical json.RawMessage `json:"canonical"`
	Digest    string          `json:"digest"`
}

// Canonical validates document, one of {"contract"}, {"schema"} or {"metadata"}, and returns its canonical form and digest.
func Canonical(ctx context.Context, document any) (CanonicalForm, error) {
	var canonical CanonicalForm
	err := call(ctx, func(r *C.mcptype_request) C.mcptype_response { return C.mcptype_canonical(r) }, document, &canonical)
	return canonical, err
}

// Changes lists what breaks expected if published is deployed. Empty means compatible.
func Changes(ctx context.Context, expected mcpcontract.Expectation, published *mcpcontract.Metadata) ([]Change, error) {
	var result struct {
		Changes []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"changes"`
	}
	err := call(ctx, func(r *C.mcptype_request) C.mcptype_response { return C.mcptype_changes(r) },
		map[string]any{"expected": expected, "published": published}, &result)
	changes := make([]Change, len(result.Changes))
	for i, change := range result.Changes {
		changes[i] = Change{Kind: change.Kind, Name: change.Name + change.URI, Text: change.Text}
	}
	return changes, err
}

// GenerateRequest asks for one target's typed client package: node, browser, python or go.
// Runtime files are copied into the package verbatim.
type GenerateRequest struct {
	Target                  string                           `json:"target"`
	Identity                mcpcontract.Identity             `json:"identity"`
	Requires                map[string]mcpcontract.Identity  `json:"requires,omitempty"`
	Contracts               map[string]*mcpcontract.Contract `json:"contracts,omitempty"`
	Runtime                 map[string][]byte                `json:"runtime,omitempty"`
	Protocol                string                           `json:"protocol,omitempty"`
	PackageFields           map[string]any                   `json:"package_fields,omitempty"`
	RuntimeSpecifier        string                           `json:"runtime_specifier,omitempty"`
	BrowserRuntimeSpecifier string                           `json:"browser_runtime_specifier,omitempty"`
	ProtocolSpecifier       string                           `json:"protocol_specifier,omitempty"`
	PythonModule            string                           `json:"python_module,omitempty"`
	PythonRuntimeModule     string                           `json:"python_runtime_module,omitempty"`
	Banner                  string                           `json:"banner,omitempty"`
	GoModule                string                           `json:"go_module,omitempty"`
}

// Generate returns the package files by relative path.
func Generate(ctx context.Context, request GenerateRequest) (map[string][]byte, error) {
	var result struct {
		Files map[string][]byte `json:"files"`
	}
	err := call(ctx, func(r *C.mcptype_request) C.mcptype_response { return C.mcptype_generate(r) }, request, &result)
	return result.Files, err
}

func call(ctx context.Context, f func(*C.mcptype_request) C.mcptype_response, in, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := json.Marshal(in)
	if err != nil {
		return err
	}
	native := C.mcptype_request_create((*C.char)(unsafe.Pointer(unsafe.SliceData(input))), C.size_t(len(input)))
	runtime.KeepAlive(input)
	if native == nil {
		return errors.New("cannot allocate mcptype request")
	}
	defer C.mcptype_request_free(native)
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		C.mcptype_request_cancel(native)
		close(canceled)
	})
	response := f(native)
	if !stop() {
		<-canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	output := unsafe.Slice((*byte)(unsafe.Pointer(response.data)), int(response.len))
	switch response.status {
	case C.MCPTYPE_OK:
		return json.Unmarshal(output, out)
	case C.MCPTYPE_CANCELLED:
		return context.Canceled
	default:
		return fmt.Errorf("mcptype FFI: %s", output)
	}
}
