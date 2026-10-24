package commandenv

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

func Value(env []string, key string) string {
	for _, entry := range slices.Backward(env) {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value
		}
	}
	return ""
}

func Environ(extra []string) ([]string, error) {
	for _, entry := range extra {
		if !strings.Contains(entry, "=") {
			return nil, fmt.Errorf("environment entry %q must be KEY=VALUE", entry)
		}
	}
	return append(os.Environ(), extra...), nil
}
