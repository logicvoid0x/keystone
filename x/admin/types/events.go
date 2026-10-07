package types

// Events (design §11.3).
const (
	EventTypeAllowlistUpdated = "allowlist_updated"
	EventTypeAdminChanged     = "admin_changed"
	EventTypeValidatorJailed  = "allowlist_validator_jailed"
	EventTypeAdminExec        = "admin_exec"

	AttributeKeyAdded    = "added"
	AttributeKeyRemoved  = "removed"
	AttributeKeyOld      = "old"
	AttributeKeyNew      = "new"
	AttributeKeyOperator = "operator"
	AttributeKeyMsgType  = "msg_type"
)
