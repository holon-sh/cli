package kiln

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlanFailure(t *testing.T) {
	request := Request{Source: t.TempDir(), Platforms: []string{"linux/unsupported"}}
	if _, err := Plan(t.Context(), request); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("planning diagnostic: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Plan(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("planning cancellation: %v", err)
	}
}
