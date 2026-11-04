package cloud

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/protocol"
)

const managedOrigin = "https://holon.localhost"

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

func resolveOrigin(origin, dir string, env []string) (string, error) {
	if origin = cmp.Or(origin, commandenv.Value(env, "HOLON_ORIGIN")); origin != "" {
		return origin, nil
	}
	path := filepath.Join(dir, "config.json")
	data, err := readPrivateFile(path)
	if errors.Is(err, errNotAuthenticated) {
		return managedOrigin, nil
	}
	if err != nil {
		return "", fmt.Errorf("read Holon configuration %s: %w", path, err)
	}
	var config clientConfig
	if err := protocol.DecodeJSONNonNull(data, &config); err != nil {
		return "", fmt.Errorf("read Holon configuration %s: %w", path, err)
	}
	return cmp.Or(config.Origin, managedOrigin), nil
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
