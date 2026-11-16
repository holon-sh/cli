package kiln

import (
	"context"
	"errors"
)

var ErrNotLinked = errors.New("this holon was built without Kiln, which plans how an app installs, builds and starts; " +
	"install a release build: curl -fsSL https://holonlabs.ai/install.sh | sh")

// Command is a command Kiln runs with no network in an app's image, without the app's build: Mounts names build
// contexts mounted read-only at the paths given, Overlay a build context copied over the app first, and Packages
// Python requirements the command can import besides the app's.
type Command struct {
	Argv     []string          `json:"command"`
	Mounts   map[string]string `json:"mounts,omitempty"`
	Overlay  string            `json:"overlay,omitempty"`
	Packages []string          `json:"packages,omitempty"`
}

// Image is the Dockerfile that builds the image of the app in source, started by start.
func Image(ctx context.Context, source string, start []string) (string, error) {
	return plan(ctx, map[string]any{"source": source, "image": map[string][]string{"start": start}})
}

// Run is the Dockerfile of an image that runs command in the app in source and holds only the command's stdout,
// stderr and exit status, as the files stdout, stderr and status.
func Run(ctx context.Context, source string, command Command) (string, error) {
	return plan(ctx, struct {
		Source string  `json:"source"`
		Run    Command `json:"run"`
	}{source, command})
}
