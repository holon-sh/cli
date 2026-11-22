package commandenv

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func Value(env []string, key string) string {
	value, _ := Lookup(env, key)
	return value
}

func Lookup(env []string, key string) (string, bool) {
	present := false
	for _, entry := range slices.Backward(env) {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value, true
		}
		present = present || entry == key
	}
	return "", present
}

func Environ(extra []string) []string { return append(os.Environ(), extra...) }

func TLSDirectory() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "holon", "tls"), nil
}
