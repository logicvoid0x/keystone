package types

import (
	"context"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	poatypes "github.com/OWNER/keystone/x/poa/types"
)

// AdminKeeper is the part of x/poa that authorizes MsgUpdateParams.
type AdminKeeper interface {
	CheckAdmin(ctx context.Context, signer string) error
}

// ValidatorSet is x/poa as seen by the attestation tallies.
type ValidatorSet interface {
	ActiveValidators(ctx context.Context) ([]poatypes.Operator, error)
	// GetOperator returns an operator of any power (an error if unknown).
	GetOperator(ctx context.Context, addr sdk.AccAddress) (poatypes.Operator, error)
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
