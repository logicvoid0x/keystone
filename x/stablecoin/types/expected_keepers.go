package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// AdminKeeper is the part of x/poa that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// BankKeeper is the part of x/bank used by x/stablecoin.
type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
	GetSupply(ctx context.Context, denom string) sdk.Coin
}
