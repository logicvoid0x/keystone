package types

import "context"

// AdminKeeper is the part of x/poa that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}
