// Package selfupdate keeps the holon that install.sh installed at the newest release.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/holon-sh/cli/internal/clientutil"
)

const (
	script    = "https://holonlabs.ai/install.sh"
	releases  = "https://api.github.com/repos/holon-sh/cli/releases?per_page=1"
	downloads = "https://github.com/holon-sh/cli/releases/download"
	interval  = time.Hour
	maxBytes  = 256 << 20
)

var client = &http.Client{Timeout: 10 * time.Minute}

// Command returns holon update, which replaces the holon that install.sh installed with the newest release.
func Command(version string) *cobra.Command {
	return &cobra.Command{Use: "update", Short: "Update holon to the newest release", Long: "Update holon to the newest release.\n\nA holon that " + script + " installed also updates itself in the background, at most once an hour. One installed with npm or pip keeps the version its project pins.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := installed()
		if err != nil {
			return err
		}
		newest, err := newestRelease(cmd.Context())
		if err != nil {
			return err
		}
		if newest == version {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "holon %s is the newest release\n", version)
			return err
		}
		if err := install(cmd.Context(), downloads, newest, path); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Updated holon %s to %s\n", version, newest)
		return err
	}}
}

// Start runs holon update in the background when install.sh installed this holon and the last check is
// over an hour old. The update takes effect from the next run.
func Start() {
	path, err := installed()
	if err != nil {
		return
	}
	checked := filepath.Join(filepath.Dir(path), "checked")
	if info, err := os.Stat(checked); err == nil && time.Since(info.ModTime()) < interval {
		return
	}
	if os.WriteFile(checked, nil, 0o644) != nil {
		return
	}
	cmd := exec.Command(path, "update")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

func installed() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", err
	}
	if path, err := filepath.EvalSymlinks(filepath.Join(home, ".local", "share", "holon", "holon")); err != nil || path != exe {
		return "", fmt.Errorf("%s was not installed by %s; update it the way you installed it", exe, script)
	}
	return exe, nil
}

func newestRelease(ctx context.Context) (string, error) {
	body, err := get(ctx, releases)
	if err != nil {
		return "", err
	}
	var list []struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("%s: %w", releases, err)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("%s lists no release", releases)
	}
	return strings.TrimPrefix(list[0].TagName, "v"), nil
}

func install(ctx context.Context, base, version, path string) error {
	archive := fmt.Sprintf("holon_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sums, err := get(ctx, base+"/v"+version+"/SHA256SUMS")
	if err != nil {
		return err
	}
	data, err := get(ctx, base+"/v"+version+"/"+archive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if !slices.Contains(strings.Split(string(sums), "\n"), hex.EncodeToString(sum[:])+"  "+archive) {
		return fmt.Errorf("%s does not match its SHA256SUMS entry", archive)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s: %w", archive, err)
	}
	files := tar.NewReader(gz)
	for {
		header, err := files.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s has no holon", archive)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", archive, err)
		}
		if header.Name == "holon" {
			return replace(path, files)
		}
	}
}

func replace(path string, binary io.Reader) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".holon-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = io.Copy(file, binary)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(file.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	return err
}

func get(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	body, err := clientutil.ReadLimited(response.Body, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	return body, nil
}
