package installation

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/protocol"
)

var errNoInstallation = errors.New("no Holon installation selected; run holon login <team>.cloud.holon-labs.dev")

type clientConfig struct {
	Origin string `json:"origin"`
}

func clientConfigDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate Holon configuration: %w", err)
	}
	return filepath.Join(dir, "holon"), nil
}

// canonicalOrigin reads an installation origin, where the scheme defaults to
// https and one trailing slash is ignored.
func canonicalOrigin(raw string) (string, error) {
	origin := strings.TrimSuffix(raw, "/")
	if !strings.Contains(origin, "://") {
		origin = protocol.HTTPSPrefix + origin
	}
	canonical, err := protocol.CanonicalOrigin(origin)
	if err != nil {
		return "", fmt.Errorf("origin %q is invalid: expected HOST[:PORT] or https://HOST[:PORT]", raw)
	}
	return canonical, nil
}

func resolveOrigin(origin, dir string, env []string) (string, error) {
	if origin = cmp.Or(origin, commandenv.Value(env, "HOLON_ORIGIN")); origin != "" {
		return canonicalOrigin(origin)
	}
	path := filepath.Join(dir, "config.json")
	data, err := readPrivateFile(path)
	if errors.Is(err, errNotAuthenticated) {
		return "", errNoInstallation
	}
	if err != nil {
		return "", fmt.Errorf("read Holon configuration %s: %w", path, err)
	}
	var config clientConfig
	if err := protocol.DecodeJSONNonNull(data, &config); err != nil {
		return "", fmt.Errorf("read Holon configuration %s: %w", path, err)
	}
	if config.Origin == "" {
		return "", errNoInstallation
	}
	return canonicalOrigin(config.Origin)
}

func saveOrigin(config ClientConfig, origin string) error {
	dir, err := clientConfigDir(config.ConfigDir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(clientConfig{Origin: origin})
	if err := writePrivateFile(path, data); err != nil {
		return fmt.Errorf("save Holon configuration %s: %w", path, err)
	}
	return nil
}
