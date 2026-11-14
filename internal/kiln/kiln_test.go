//go:build kiln

package kiln

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlanFailure(t *testing.T) {
	request := Request{Source: t.TempDir()}
	if _, err := Image(t.Context(), request); err == nil || !strings.Contains(err.Error(), "no provider detected") {
		t.Fatalf("planning diagnostic: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Image(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("planning cancellation: %v", err)
	}
}
