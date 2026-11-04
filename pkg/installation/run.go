package installation

import (
	"k8s.io/utils/clock"

	"github.com/holon-sh/protocol"
)

type commandConfig struct {
	Auth  ClientConfig
	Env   []string
	Clock clock.WithTicker
}

func ControlPlane(name string) protocol.Plane {
	if name == protocol.ProductionName {
		return protocol.AdminPlane
	}
	return protocol.DevPlane
}
