package types

// Events (design §11.3).
const (
	EventTypeValidatorSetUpdated = "validator_set_updated"
	EventTypeAdminChanged        = "admin_changed"

	AttributeKeyAdded   = "added"
	AttributeKeyRemoved = "removed"
	AttributeKeyOld     = "old"
	AttributeKeyNew     = "new"
)
