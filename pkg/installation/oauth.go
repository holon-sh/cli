package installation

import (
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

type secretReference mcpregistry.Value

type manifestOAuth struct {
	ClientID     protocol.Template `description:"Client ID; {NAME} stands for a configuration input." json:"clientID" required:"true"`
	ClientSecret secretReference   `json:"clientSecret,omitempty"`
}

func (c *manifestOAuth) client() *protocol.OAuthClient {
	if c == nil {
		return nil
	}
	return &protocol.OAuthClient{ClientID: c.ClientID, ClientSecret: c.ClientSecret.Secret}
}
