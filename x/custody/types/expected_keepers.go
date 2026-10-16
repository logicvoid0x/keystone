package types

import (
	"context"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/validators"
)

// AdminKeeper is the part of x/admin that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// ValidatorSet is the app validator set (app/valset over x/staking) as seen by custody
// (design §4.2, §9.2). ActiveValidators are bonded, unjailed, power > 0, sorted by
// operator address.
type ValidatorSet interface {
	ActiveValidators(ctx context.Context) ([]validators.Validator, error)
	// GetOperator returns a validator of any status (an error if unknown).
	GetOperator(ctx context.Context, addr sdk.AccAddress) (validators.Validator, error)
}

// VaultKeeper is x/vault as seen by deposit crediting and reversal (design §9.7).
type VaultKeeper interface {
	// CreditCollateral credits sat within the cap headroom and returns the satoshis
	// credited; the rest goes to the owner's claimable balance.
	CreditCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (sdkmath.Int, error)
	// DebitCollateral removes up to sat and returns the shortfall.
	DebitCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (sdkmath.Int, error)
	// VaultOwner returns the owner and whether the vault can still take collateral.
	VaultOwner(ctx context.Context, id uint64) (owner sdk.AccAddress, open bool, err error)
	// MinDepositSat is the vault param below which deposits go to the fee reserve.
	MinDepositSat(ctx context.Context) (sdkmath.Int, error)
	// OpenVaultIDs lists the vaults that are not CLOSED (M4b: registered for a new
	// epoch's deposit scripts).
	OpenVaultIDs(ctx context.Context) ([]uint64, error)
}

// StakingSlasher is x/staking and x/slashing as seen by theft slashing (design §9.12,
// M7): slash operator's validator by fraction for an infraction at infractionHeight
// (unbonding entries since then included), jail and tombstone it; return the tokens
// burned.
type StakingSlasher interface {
	SlashForTheft(ctx context.Context, operator sdk.AccAddress, infractionHeight int64, fraction sdkmath.LegacyDec) (sdkmath.Int, error)
}
