package types

import (
	"context"

	poatypes "github.com/OWNER/keystone/x/poa/types"
)

// AdminKeeper is the part of x/poa that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// ValidatorSet is x/poa as seen by the oracle (design §4.2).
type ValidatorSet interface {
	ActiveValidators(ctx context.Context) ([]poatypes.Operator, error)
	TotalPower(ctx context.Context) (int64, error)
}
