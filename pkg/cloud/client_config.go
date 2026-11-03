package cloud

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/holon-sh/cli/pkg/commandenv"
	"github.com/holon-sh/cli/pkg/protocol"
)

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
	origin = cmp.Or(origin, commandenv.Value(env, "HOLON_ORIGIN"))
	if origin == "" {
		path := filepath.Join(dir, "config.json")
		data, err := readPrivateFile(path)
		if errors.Is(err, errNotAuthenticated) {
			return commandenv.ManagedOrigin, nil
		}
		if err != nil {
			return "", fmt.Errorf("read Holon configuration %s: %w", path, err)
		}
		var config clientConfig
		if err := protocol.DecodeJSONNonNull(data, &config); err != nil {
			return "", fmt.Errorf("read Holon configuration %s: %w", path, err)
		}
		origin = config.Origin
	}
	if origin == "" {
		return commandenv.ManagedOrigin, nil
	}
	return origin, nil
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

func SelectInstallation(ctx context.Context, origin, version string, stdout io.Writer, configDir string, httpClient *http.Client) error {
	var client *apiClient
	var info protocol.Info
	var lastErr error
	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		client, lastErr = newAPIClient(origin, httpClient, 0)
		if lastErr == nil {
			info, lastErr = client.Info(ctx)
		}
		if lastErr != nil {
			return false, nil
		}
		if info.Version != version {
			return false, fmt.Errorf("server reports %s; expected %s", info.Version, version)
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("a Holon installation exists but %s is not reachable at the matching version; check ingress: %w", origin, errors.Join(err, lastErr))
	}
	if err := saveOrigin(ClientConfig{ConfigDir: configDir}, info.Installation.Origin); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Holon %s is ready at %s\nSelected Holon installation %s\n",
		version, origin, info.Installation.Origin)
	return err
}
