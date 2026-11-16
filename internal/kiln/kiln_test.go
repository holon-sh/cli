//go:build kiln

package kiln

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlanFailure(t *testing.T) {
	source := t.TempDir()
	if _, err := Image(t.Context(), source, nil); err == nil || !strings.Contains(err.Error(), "no provider detected") {
		t.Fatalf("planning diagnostic: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Image(ctx, source, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("planning cancellation: %v", err)
	}
}
