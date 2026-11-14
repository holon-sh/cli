package kiln

import (
	"context"
	"errors"
)

var ErrNotLinked = errors.New("this holon was built without Kiln, which plans how an app installs, builds and starts; " +
	"install a release build: curl -fsSL https://holonlabs.ai/install.sh | sh")

type Request struct {
	ConfigFilePath string   `json:"configFilePath,omitempty"`
	Source         string   `json:"source"`
	Platforms      []string `json:"platforms"`
	Start          []string `json:"start,omitempty"`
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
