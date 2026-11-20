package types

import (
	"context"

	"github.com/logicvoid0x/keystone/x/validators"
)

// AdminKeeper is the part of x/admin that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// ValidatorSet is the app validator set (app/valset over x/staking) as seen by the
// oracle (design §4.2): bonded, unjailed validators with power > 0, sorted by operator
// address, and their total power.
type ValidatorSet interface {
	ActiveValidators(ctx context.Context) ([]validators.Validator, error)
	TotalPower(ctx context.Context) (int64, error)
}
