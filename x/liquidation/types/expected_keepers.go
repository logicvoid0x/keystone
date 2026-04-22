package types

import (
	"context"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// AdminKeeper is the part of x/poa that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// OracleKeeper is x/oracle as seen by x/liquidation.
type OracleKeeper interface {
	LiquidationPrice(ctx context.Context) (sdkmath.Int, bool, error)
	IsActiveForLiquidation(ctx context.Context) (bool, error)
}

// VaultKeeper is x/vault as seen by x/liquidation (design §4.2).
type VaultKeeper interface {
	GetVault(ctx context.Context, id uint64) (vaulttypes.Vault, error)
	Debt(ctx context.Context, v vaulttypes.Vault) (sdkmath.Int, error)
	IsLiquidatable(ctx context.Context, v vaulttypes.Vault, price sdkmath.Int) (bool, error)
	AnyLiquidatable(ctx context.Context, price sdkmath.Int) (bool, error)
	IterateLiquidatable(ctx context.Context, price sdkmath.Int, fn func(vaulttypes.Vault) (stop bool)) error
	CloseForLiquidation(ctx context.Context, id uint64, seizeSat sdkmath.Int) (debt sdkmath.Int, err error)
	CreditCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (sdkmath.Int, error)
}

// StablecoinKeeper is x/stablecoin as seen by x/liquidation.
type StablecoinKeeper interface {
	BurnFromModule(ctx context.Context, module string, amt sdkmath.Int) error
	RecordBadDebt(ctx context.Context, amt sdkmath.Int, source string) error
	BurnAgainstSin(ctx context.Context, module string, amt sdkmath.Int) error
	AddToSurplus(ctx context.Context, fromModule string, amt sdkmath.Int) error
	CoverShortfall(ctx context.Context, to string, amt sdkmath.Int) (sdkmath.Int, error)
}

// CustodyKeeper is x/custody as seen by x/liquidation.
type CustodyKeeper interface {
	CreditClaimable(ctx context.Context, addr sdk.AccAddress, sat sdkmath.Int) error
}

// BankKeeper moves KUSD into and out of the pool module account.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}
