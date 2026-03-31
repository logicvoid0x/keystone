package types

import (
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/internal/bounds"
)

var (
	// MaxTreasuryCapSat is 100 BTC (design §17).
	MaxTreasuryCapSat = sdkmath.NewInt(100 * 100_000_000)
	// MinWithdrawSatFloor is Bitcoin's P2TR dust limit; MinWithdrawSatCeil is 1 BTC.
	MinWithdrawSatFloor = sdkmath.NewInt(330)
	MinWithdrawSatCeil  = sdkmath.NewInt(100_000_000)
)

// DefaultParams are the design §10.3 defaults (testnet values; localnet genesis
// overrides MembershipDelayBlocks and ConfirmDepth, §9.1).
func DefaultParams() Params {
	return Params{
		TreasuryCapSat:        sdkmath.NewInt(1_000_000_000),
		MembershipDelayBlocks: 28800,
		ConfirmDepth:          6,
		NoncePool:             8,
		SessionDeadline:       5,
		MaxInputs:             8,
		BatchMinRequests:      16,
		BatchMaxWait:          20,
		FeerateMin:            1,
		FeerateMax:            200,
		RbfAfterBtcBlocks:     6,
		MinWithdrawSat:        sdkmath.NewInt(10_000),
		EmergencyRekey:        false,
	}
}

// Validate enforces the bounds. TreasuryCapSat <= 100 BTC and MembershipDelayBlocks
// >= 100 are from design §17; the others are sanity bounds recorded in docs/decisions.md.
func (p Params) Validate() error {
	err := bounds.First(
		bounds.Int("treasury_cap_sat", p.TreasuryCapSat, sdkmath.ZeroInt(), MaxTreasuryCapSat),
		bounds.Uint("membership_delay_blocks", p.MembershipDelayBlocks, 100, 1_000_000),
		bounds.Uint("confirm_depth", uint64(p.ConfirmDepth), 1, 100),
		bounds.Uint("nonce_pool", uint64(p.NoncePool), 1, 64),
		bounds.Uint("session_deadline", uint64(p.SessionDeadline), 1, 100),
		bounds.Uint("max_inputs", uint64(p.MaxInputs), 1, 64),
		bounds.Uint("batch_min_requests", uint64(p.BatchMinRequests), 1, 1000),
		bounds.Uint("batch_max_wait", uint64(p.BatchMaxWait), 1, 10_000),
		bounds.Uint("feerate_max", p.FeerateMax, 1, 10_000),
		bounds.Uint("feerate_min", p.FeerateMin, 1, p.FeerateMax),
		bounds.Uint("rbf_after_btc_blocks", uint64(p.RbfAfterBtcBlocks), 1, 1000),
		bounds.Int("min_withdraw_sat", p.MinWithdrawSat, MinWithdrawSatFloor, MinWithdrawSatCeil),
	)
	if err != nil {
		return errorsmod.Wrap(ErrInvalidParams, err.Error())
	}
	return nil
}
