package kiln

import "errors"

var ErrNotLinked = errors.New("this holon was built without Kiln, which plans how an app installs, builds and starts; " +
	"install a release build: curl -fsSL https://holonlabs.ai/install.sh | sh")

type Request struct {
	ConfigFilePath string   `json:"configFilePath,omitempty"`
	Source         string   `json:"source"`
	Platforms      []string `json:"platforms"`
	Start          []string `json:"start,omitempty"`
	Provider       Provider `json:"provider,omitempty"`
	Development    bool     `json:"development,omitempty"`
}

// Provider names the part of a project Kiln plans; by default Kiln detects it.
type Provider string

const (
	Node   Provider = "node"
	Python Provider = "python"
)

// Result is Kiln's plan for an app: the Dockerfile that builds its image and, for a development request, the steps a
// development environment runs.
type Result struct {
	Dockerfile string `json:"dockerfile"`
	Steps      Steps  `json:"steps"`
}

// Steps are what a development environment runs in an app's source: Install, then Build.
type Steps struct {
	Install [][]string `json:"install"`
	Build   [][]string `json:"build"`
}
