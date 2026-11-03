package protocol

import (
	"errors"
	"strings"
)

func AppPath(name string) string {
	return MCPPathPrefix + name
}

type Plane string

const (
	BasePlane    Plane = "base"
	DevPlane     Plane = "dev"
	AdminPlane   Plane = "admin"
	SessionPlane Plane = "session"
)

var planeLabels = map[Plane]string{BasePlane: "", DevPlane: "dev.", AdminPlane: "admin."}

func PlaneOrigin(origin string, plane Plane, sessionID string) (string, error) {
	canonical, err := CanonicalOrigin(origin)
	if err != nil {
		return "", err
	}
	label, ok := planeLabels[plane]
	if plane == SessionPlane {
		if !ValidDNSLabel(sessionID) || sessionID == "dev" || sessionID == "admin" {
			return "", errors.New("session id is invalid")
		}
		label, ok = sessionID+".", true
	}
	if !ok {
		return "", errors.New("plane is invalid")
	}
	return HTTPSPrefix + label + strings.TrimPrefix(canonical, HTTPSPrefix), nil
}

func ParseAppPath(path string) (string, bool) {
	name, ok := strings.CutPrefix(path, MCPPathPrefix)
	return name, ok && ValidAppName(name)
}

var sessionPlanes = map[string]Plane{ModeProduction: BasePlane, ModeDevelopment: SessionPlane}

func SessionAudience(origin, mode, sessionID string) (string, error) {
	plane, ok := sessionPlanes[mode]
	if !ok {
		return "", errors.New("session mode is invalid")
	}
	return PlaneOrigin(origin, plane, sessionID)
}
