package kiln

import "errors"

var ErrNotLinked = errors.New("this holon was built without Kiln, which holon deploy needs to build images; " +
	"install a release build: curl -fsSL https://holonlabs.ai/install.sh | sh")

type Request struct {
	ConfigFilePath string   `json:"configFilePath,omitempty"`
	Source         string   `json:"source"`
	Platforms      []string `json:"platforms"`
	Start          []string `json:"start,omitempty"`
}
