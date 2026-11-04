module github.com/holon-sh/cli

go 1.27.0

require (
	github.com/distribution/reference v0.6.0
	github.com/gofrs/flock v0.13.1
	github.com/moby/patternmatcher v0.6.1
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/spf13/cobra v1.10.2
	oras.land/oras-go/v2 v2.6.2
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/holon-sh/mcp-bridge v0.0.4 // indirect
	github.com/holon-sh/protocol v0.0.1-dev.1 // indirect
	github.com/holon-sh/runtime v0.0.1-dev.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace github.com/holon-sh/protocol => github.com/hughhan1/protocol-private v0.0.0-20261104024740-e6be7e8561be

replace github.com/holon-sh/runtime => github.com/hughhan1/runtime-private v0.0.0-20261104025022-3926d2564c82

tool github.com/holon-sh/runtime/cmd/holon-mcp-runtime
