package cloud

import (
	"errors"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

type manifestOAuth protocol.OAuthClient

func (c *manifestOAuth) UnmarshalJSON(data []byte) error {
	var fields struct {
		ClientID     protocol.Template `json:"clientID"`
		ClientSecret mcpregistry.Value `json:"clientSecret,omitempty"`
	}
	if err := protocol.DecodeJSONNonNull(data, &fields); err != nil {
		return err
	}
	c.ClientID = fields.ClientID
	if fields.ClientSecret.Value != nil {
		return errors.New("oauth.clientSecret requires a secret reference")
	}
	c.ClientSecret = fields.ClientSecret.Secret
	return (*protocol.OAuthClient)(c).Validate(nil)
}
