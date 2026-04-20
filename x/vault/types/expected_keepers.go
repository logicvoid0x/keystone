package types

import (
	"context"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	custodytypes "github.com/OWNER/keystone/x/custody/types"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
)

// AdminKeeper is the part of x/poa that authorizes admin messages.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// OracleKeeper is x/oracle as seen by x/vault (design §4.2).
type OracleKeeper interface {
	GetPriceState(ctx context.Context) (oracletypes.PriceState, error)
	CollateralPrice(ctx context.Context) (sdkmath.Int, bool, error)
}

// StablecoinKeeper is x/stablecoin as seen by x/vault.
type StablecoinKeeper interface {
	Mint(ctx context.Context, to sdk.AccAddress, amt sdkmath.Int) error
	BurnFrom(ctx context.Context, from sdk.AccAddress, amt sdkmath.Int) error
	AccrueSurplus(ctx context.Context, amt sdkmath.Int) error
	AreMintsPaused(ctx context.Context) (bool, error)
}

// CustodyKeeper is x/custody as seen by x/vault (M1 stub surface).
type CustodyKeeper interface {
	GetParams(ctx context.Context) (custodytypes.Params, error)
	CreditClaimable(ctx context.Context, addr sdk.AccAddress, sat sdkmath.Int) error
	DebitClaimable(ctx context.Context, addr sdk.AccAddress, sat sdkmath.Int) error
	CapHeadroom(ctx context.Context) (sdkmath.Int, error)
	AddCredited(ctx context.Context, sat sdkmath.Int) error
	SubCredited(ctx context.Context, sat sdkmath.Int) error
}
