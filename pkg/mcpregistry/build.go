package mcpregistry

import (
	"maps"

	"github.com/holon-sh/cli/pkg/protocol"
)

func BuildRecord(app protocol.App, catalogEnv map[string]protocol.Input, descriptor *Descriptor) protocol.BuildRecord {
	record := protocol.BuildRecord{
		SchemaVersion: 1, AppName: app.Name,
		Runtime: protocol.BuildRuntime{
			Icons:        app.Icons,
			Port:         app.Port,
			Args:         app.Args,
			EnvTemplates: app.EnvTemplates,
			Env:          map[string]protocol.Input{},
			Requires:     app.Requires,
			Egress:       app.Egress,
		},
		UnsupportedReasons: []string{},
	}
	maps.Copy(record.Runtime.Env, catalogEnv)
	for name, input := range app.Env {
		if _, declared := record.Runtime.Env[name]; declared {
			continue
		}
		record.Runtime.Env[name] = protocol.Input{IsRequired: input.IsRequired || input.Value != nil, IsSecret: input.IsSecret}
	}
	if app.OAuth != nil {
		record.UnsupportedReasons = append(record.UnsupportedReasons, "This build requires deployment OAuth configuration; deploy it with the CLI.")
	}
	if descriptor != nil {
		record.ServerName, record.ServerVersion = descriptor.Name, descriptor.Version
		record.Title, record.Description = descriptor.Title, descriptor.Description
	}

	return record
}
