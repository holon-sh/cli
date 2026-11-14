package kiln

import (
	"context"
	"errors"
)

var ErrNotLinked = errors.New("this holon was built without Kiln, which plans how an app installs, builds and starts; " +
	"install a release build: curl -fsSL https://holonlabs.ai/install.sh | sh")

type Request struct {
	Source string
	Start  []string
}

// Command is a command Kiln runs with no network in an app's image, without the app's build: Mounts names build
// contexts mounted read-only at the paths given, Overlay a build context copied over the app first, and Packages
// Python requirements the command can import besides the app's.
type Command struct {
	Argv     []string          `json:"command"`
	Mounts   map[string]string `json:"mounts,omitempty"`
	Overlay  string            `json:"overlay,omitempty"`
	Packages []string          `json:"packages,omitempty"`
}

// Steps are what a development environment runs in an app's source: Install, then Build.
type Steps struct {
	Install [][]string `json:"install"`
	Build   [][]string `json:"build"`
}

type startup struct {
	Start []string `json:"start,omitempty"`
}

// Image is the Dockerfile that builds the image of the app request names.
func Image(ctx context.Context, request Request) (string, error) {
	return dockerfile(ctx, struct {
		Source string  `json:"source"`
		Image  startup `json:"image"`
	}{request.Source, startup{request.Start}})
}

// Development is what a development environment runs for the app in source.
func Development(ctx context.Context, source string) (Steps, error) {
	var steps Steps
	err := plan(ctx, struct {
		Source      string   `json:"source"`
		Development struct{} `json:"development"`
	}{Source: source}, &steps)
	return steps, err
}

// Run is the Dockerfile of an image that runs command in the app in source and holds only the command's stdout,
// stderr and exit status, as the files stdout, stderr and status.
func Run(ctx context.Context, source string, command Command) (string, error) {
	return dockerfile(ctx, struct {
		Source string  `json:"source"`
		Run    Command `json:"run"`
	}{source, command})
}

func dockerfile(ctx context.Context, request any) (string, error) {
	var result struct {
		Dockerfile string `json:"dockerfile"`
	}
	err := plan(ctx, request, &result)
	return result.Dockerfile, err
}
