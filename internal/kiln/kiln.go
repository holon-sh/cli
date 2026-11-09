package kiln

import "errors"

var ErrNotLinked = errors.New("this holon was built without Kiln, which holon dev and holon deploy need to build images; " +
	"install a release build: curl -fsSL https://holon-labs.dev/install.sh | sh")

type Request struct {
	ConfigFilePath string   `json:"configFilePath,omitempty"`
	Source         string   `json:"source"`
	Platforms      []string `json:"platforms"`
	Start          []string `json:"start,omitempty"`
	Development    bool     `json:"development,omitempty"`
}

type Result struct {
	Dockerfile string `json:"dockerfile"`
}
