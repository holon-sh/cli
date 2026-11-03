package protocol

const (
	ToolCreateSession      = "create_session"
	ToolGetSession         = "get_session"
	ToolUpdateSession      = "update_session"
	ToolDeleteSession      = "delete_session"
	ToolRenewSession       = "renew_session"
	ToolAuthorizeLifecycle = "authorize_lifecycle"
	ToolRetryLifecycle     = "retry_lifecycle"
	ToolUploadGrant        = "issue_upload_grant"
	ToolSetValue           = "set_value"
	ToolDeleteValue        = "delete_value"
)

const MCPCallEvent = "holon.mcp.call"

type SessionRequest struct {
	Session string `json:"session"`
}

type WritePrecondition struct {
	ExpectedRevision string `json:"expectedRevision,omitempty"`
	CreateOnly       bool   `json:"createOnly,omitempty"`
}

type DeleteSessionRequest struct {
	SessionRequest

	ExpectedRevision string `json:"expectedRevision"`
	SkipHooks        bool   `json:"skipHooks,omitempty"`
}

type AuthorizeLifecycleRequest struct {
	SessionRequest

	Operation   string `json:"operation"`
	AccessToken string `json:"accessToken,omitempty"`
}

type RetryLifecycleRequest struct {
	SessionRequest

	Operation        string `json:"operation"`
	ExpectedRevision string `json:"expectedRevision"`
}

type AppRequest struct {
	SessionRequest

	Environment string `json:"environment,omitempty"`
	App         string `json:"app,omitempty"`
}

type SessionLogsRequest struct {
	AppRequest

	Operation string `json:"operation,omitempty"`
	Hook      string `json:"hook,omitempty"`
	Attempt   *int64 `json:"attempt,omitempty"`
	Event     string `json:"event,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Status    string `json:"status,omitempty"`
	Since     string `json:"since,omitempty"`
	TraceID   string `json:"traceId,omitempty"`
	Text      string `json:"text,omitempty"`
}

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type UpdateSessionRequestFor[Desired any] struct {
	SessionRequest
	WritePrecondition

	Desired Desired `json:"desired"`
}

type DeleteSessionResultFor[Session any] struct {
	Deleted bool     `json:"deleted"`
	Session *Session `json:"session,omitempty"`
}

type AppsRequestFor[A any] struct {
	RequestID  string `json:"requestId,omitempty"`
	TTLSeconds *int64 `json:"ttlSeconds,omitempty" maximum:"86400" minimum:"1"`
	Apps       []A    `json:"apps"                 minItems:"1"    nullable:"false" required:"true"`
}

func (input *AppsRequestFor[A]) UnmarshalJSON(data []byte) error {
	type plain AppsRequestFor[A]
	return DecodeJSONFields(data, (*plain)(input), "apps")
}

type VariableRequest struct {
	Session  string `json:"session,omitempty"`
	Kind     string `json:"kind"`
	Variable string `json:"variable"`
}

type SetValueRequest struct {
	VariableRequest

	Value string `json:"value"`
}

type UploadRequest struct {
	Apps []string `json:"apps" minItems:"1" nullable:"false" required:"true"`
}

type UploadGrant struct {
	Grant        string            `json:"grant"`
	Registry     string            `json:"registry"`
	Repositories map[string]string `json:"repositories"`
	ExpiresAt    string            `json:"expiresAt"`
}
