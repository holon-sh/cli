module github.com/holon-sh/cli

go 1.27.0

require (
	github.com/distribution/reference v0.6.0
	github.com/gofrs/flock v0.13.1
	github.com/holon-sh/mcp-bridge v0.0.4
	github.com/holon-sh/protocol v0.0.1-dev.1
	github.com/holon-sh/runtime v0.0.1-dev.1
	github.com/moby/patternmatcher v0.6.1
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/spf13/cobra v1.10.2
	github.com/zitadel/oidc/v3 v3.51.11
	golang.org/x/oauth2 v0.37.0
	golang.org/x/term v0.46.0
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3
	oras.land/oras-go/v2 v2.6.2
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/muhlemmer/gu v0.3.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/zitadel/schema v1.3.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)

replace github.com/holon-sh/protocol => github.com/hughhan1/protocol-private v0.0.0-20261104024740-e6be7e8561be

replace github.com/holon-sh/runtime => github.com/hughhan1/runtime-private v0.0.0-20261104025022-3926d2564c82
