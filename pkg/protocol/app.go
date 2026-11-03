package protocol

import (
	"cmp"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Template = string

type App struct {
	Icons []mcp.Icon `json:"icons,omitempty"`

	Mount string `json:"mount,omitempty"`

	Release      *Release            `json:"release,omitempty"`
	OAuth        *OAuthClient        `json:"oauth,omitempty"`
	Remote       *Remote             `json:"remote,omitempty"`
	Access       *AppAccess          `json:"access"           nullable:"false" required:"true"`
	Name         string              `json:"name" maxLength:"49" pattern:"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" required:"true"`
	Image        string              `json:"image,omitempty" pattern:"@sha256:[0-9a-f]{64}$"`
	Port         int32               `json:"port,omitempty" maximum:"65535" minimum:"1"`
	Args         []Template          `json:"args,omitempty" maxItems:"64"`
	Requires     []string            `json:"requires,omitempty"`
	Egress       Egress              `json:"egress,omitzero"`
	Env          map[string]Input    `json:"env,omitempty"`
	EnvTemplates map[string]Template `json:"envTemplates,omitempty"`
	Applied      map[string]string   `json:"applied,omitempty"`
}

type Input struct {
	IsRequired bool    `json:"isRequired,omitempty"`
	IsSecret   bool    `json:"isSecret,omitempty"`
	Value      *string `json:"value,omitempty"`
	Default    *string `json:"default,omitempty"`
}

type AccessRule struct {
	OAuth *OAuthRequirement `json:"oauth,omitempty"`
}

type OAuthRequirement struct {
	Scopes []string `description:"OAuth scope tokens, at most 256 bytes." items.maxLength:"256" items.minLength:"1" json:"scopes,omitempty" uniqueItems:"true"`
}

type AppAccess struct {
	Callers []string `description:"App callers: omit for all; [] denies all." json:"callers,omitzero" maxItems:"256" nullable:"false" uniqueItems:"true"`

	Server *AccessRule           `description:"{} is public; oauth: {} requires authentication." json:"server" nullable:"false" required:"true"`
	Tools  map[string]AccessRule `description:"Tool rules combined with server requirements." json:"tools,omitempty" maxProperties:"256"`
}

type OAuthClient struct {
	ClientID     string `json:"clientID" required:"true"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

type Remote struct {
	URL     string              `json:"url" required:"true"`
	Headers map[string]Template `json:"headers,omitempty"`
}

type EgressDestination struct {
	Host  Template `description:"Exact DNS hostname or unicast IP address; {NAME} stands for a non-secret input." json:"host" required:"true"`
	Ports []int32  `description:"Nonempty list of TCP ports from 1 to 65535." items.maximum:"65535" items.minimum:"1" json:"ports" minItems:"1" required:"true"`
}

type Egress []EgressDestination

type Hook struct {
	Event    string `json:"event"`
	Version  string `json:"version,omitempty"`
	Path     string `json:"path"`
	Runtime  string `json:"runtime"`
	Checksum string `json:"checksum"`
}

func (h Hook) Key() string {
	if h.Version == "" {
		return h.Event
	}
	return h.Version + "/" + h.Event
}

type Artifact struct {
	Architecture string `json:"architecture"`
	WorkingDir   string `json:"workingDir"`
	Hooks        []Hook `json:"hooks,omitempty"`
}

type Release struct {
	Name      string     `json:"name,omitempty"`
	Version   string     `json:"version,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

func (i *Input) UnmarshalJSON(data []byte) error {
	type plain Input
	if err := DecodeJSONNonNull(data, (*plain)(i)); err != nil {
		return err
	}
	return i.Validate()
}

func (i Input) Validate() error {
	value := cmp.Or(i.Value, i.Default)
	if i.Value != nil && i.Default != nil {
		return errors.New("input cannot declare both value and default")
	}
	if i.IsSecret && value != nil {
		return errors.New("secret inputs must use the secret store, without literal values or defaults")
	}
	if value != nil && !ValidInputValue(*value) {
		return errors.New("input values must be single-line text without NUL characters")
	}
	return nil
}

func ValidInputValue(value string) bool {
	return !strings.ContainsAny(value, "\x00\r\n")
}

func (access *AppAccess) UnmarshalJSON(data []byte) error {
	type plainAccess AppAccess
	return DecodeJSONNonNull(data, (*plainAccess)(access))
}
