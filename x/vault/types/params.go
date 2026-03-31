package types

import (
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/internal/bounds"
)

var (
	// RAY is 1e27 fixed point.
	RAY = sdkmath.NewIntWithDecimal(1, 27)
	// DefaultStabilityFeePerSecond is 1.02^(1/31,536,000) in RAY: 2% per year (design §7.1).
	DefaultStabilityFeePerSecond = mustInt("1000000000627937192491029810")
	// MaxStabilityFeePerSecond is 2^(1/31,536,000) in RAY: 100% per year.
	MaxStabilityFeePerSecond = mustInt("1000000021979553151239153027")

	ukusd = sdkmath.NewInt(1_000_000) // 1 KUSD
	// MaxGlobalDebtCeiling is 1e9 KUSD.
	MaxGlobalDebtCeiling = ukusd.MulRaw(1_000_000_000)
	// MinDepositSatFloor is Bitcoin's P2TR dust limit; MinDepositSatCeil is 1 BTC.
	MinDepositSatFloor = sdkmath.NewInt(330)
	MinDepositSatCeil  = sdkmath.NewInt(100_000_000)
)

// DefaultParams are the design §7.1 / §10.3 defaults.
func DefaultParams() Params {
	return Params{
		McrBps:                15000,
		LrBps:                 13000,
		StabilityFeePerSecond: DefaultStabilityFeePerSecond,
		GlobalDebtCeiling:     ukusd.MulRaw(500_000),
		Dust:                  ukusd.MulRaw(100),
		MinDepositSat:         sdkmath.NewInt(10_000),
	}
}

// Validate enforces the bounds. MCR 11000..30000 and LR below MCR are from design §17;
// the others are sanity bounds recorded in docs/decisions.md.
func (p Params) Validate() error {
	err := bounds.First(
		bounds.Uint("mcr_bps", uint64(p.McrBps), 11_000, 30_000),
		bounds.Uint("lr_bps", uint64(p.LrBps), bounds.MaxBps+1, uint64(p.McrBps)-1),
		bounds.Int("stability_fee_per_second", p.StabilityFeePerSecond, RAY, MaxStabilityFeePerSecond),
		bounds.Int("global_debt_ceiling", p.GlobalDebtCeiling, sdkmath.ZeroInt(), MaxGlobalDebtCeiling),
	)
	if err == nil {
		err = bounds.First(
			bounds.Int("dust", p.Dust, sdkmath.ZeroInt(), p.GlobalDebtCeiling),
			bounds.Int("min_deposit_sat", p.MinDepositSat, MinDepositSatFloor, MinDepositSatCeil),
		)
	}
	if err != nil {
		return errorsmod.Wrap(ErrInvalidParams, err.Error())
	}
	return nil
}

func mustInt(s string) sdkmath.Int {
	v, ok := sdkmath.NewIntFromString(s)
	if !ok {
		panic("bad integer constant " + s)
	}
	return v
}
