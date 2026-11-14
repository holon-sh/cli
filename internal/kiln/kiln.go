package kiln

import (
	"context"
	"errors"
)

var ErrNotLinked = errors.New("this holon was built without Kiln, which plans how an app installs, builds and starts; " +
	"install a release build: curl -fsSL https://holonlabs.ai/install.sh | sh")

type Request struct {
	Source string   `json:"source"`
	Start  []string `json:"start,omitempty"`
	Run    *Run     `json:"run,omitempty"`
}

// Run is a command run with no network in an app's image, without the app's build; the image is the command's
// stdout, stderr and exit status, as the files stdout, stderr and status. Mounts names build contexts mounted
// read-only at the paths given, Overlay a build context copied over the app first, and Packages Python
// requirements the command can import besides the app's.
type Run struct {
	Command  []string          `json:"command"`
	Mounts   map[string]string `json:"mounts,omitempty"`
	Overlay  string            `json:"overlay,omitempty"`
	Packages []string          `json:"packages,omitempty"`
}

// Steps are what a development environment runs in an app's source: Install, then Build.
type Steps struct {
	Install [][]string `json:"install"`
	Build   [][]string `json:"build"`
}

// Image is the Dockerfile that builds the image of the app request names.
func Image(ctx context.Context, request Request) (string, error) {
	var result struct {
		Dockerfile string `json:"dockerfile"`
	}
	err := plan(ctx, request, &result)
	return result.Dockerfile, err
}

// Development is what a development environment runs for the app in source.
func Development(ctx context.Context, source string) (Steps, error) {
	request := struct {
		Source      string `json:"source"`
		Development bool   `json:"development"`
	}{source, true}
	var steps Steps
	err := plan(ctx, request, &steps)
	return steps, err
}
