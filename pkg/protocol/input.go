package protocol

import (
	"regexp"
	"slices"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var reservedEnv = []string{
	RuntimeConfigEnv, "HOLON_HOOK_CONFIG",
	"OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL",
}

func ValidEnvName(value string) bool {
	return !slices.Contains(reservedEnv, value) && len(value) <= 128 && envName.MatchString(value)
}

func ValidEnvTemplates[I any](env map[string]I, templates map[string]Template) bool {
	for name, template := range templates {
		if _, declared := env[name]; declared || !ValidEnvName(name) || !validTemplate(env, template) {
			return false
		}
	}
	return true
}

func ValidArgs[I any](env map[string]I, args []Template) bool {
	return !slices.ContainsFunc(args, func(arg Template) bool { return !validTemplate(env, arg) })
}

func validTemplate[I any](env map[string]I, template Template) bool {
	_, complete := Expand(template, func(name string) (string, bool) {
		_, declared := env[name]
		return "", declared
	})
	return ValidInputValue(template) && complete
}

func (i Input) Kind() string {
	if i.IsSecret {
		return "secret"
	}
	return "config"
}

func (i Input) Source(revision string) (*string, string) {
	if i.Value != nil {
		return i.Value, ""
	}
	if revision != "" {
		return nil, revision
	}
	return i.Default, ""
}
