package oauthscope_test

import (
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/oauthscope"
)

func TestScopeContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		valid  bool
	}{
		{"unscoped", nil, true},
		{"control", []string{"system:prod:read"}, true},
		{"development", []string{"read", "Read", strings.Repeat("x", 256)}, true},
		{"production", []string{"app:chat:read"}, true},
		{"duplicate", []string{"read", "read"}, false},
		{"empty", []string{""}, false},
		{"space", []string{"read write"}, false},
		{"quote", []string{"read\""}, false},
		{"backslash", []string{"read\\"}, false},
		{"unicode", []string{"réad"}, false},
		{"too long", []string{strings.Repeat("x", 257)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if oauthscope.ValidScopes(tc.scopes) != tc.valid {
				t.Fatalf("ValidScopes(%q) != %t", tc.scopes, tc.valid)
			}
		})
	}
}
