package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// RemovalGuard decides whether operators may leave the allowlist (and so be jailed).
// removed is sorted by address bytes. x/custody provides the real guard (design §9.4).
type RemovalGuard interface {
	AllowRemoval(ctx context.Context, removed []sdk.AccAddress) error
}

// AllowAll is the default guard. It allows every removal.
type AllowAll struct{}

func (AllowAll) AllowRemoval(context.Context, []sdk.AccAddress) error { return nil }
