package types

import (
	"math/big"

	sdkmath "cosmossdk.io/math"
)

// Fixed-point scales.
var (
	WAD = sdkmath.NewIntWithDecimal(1, 18)
	// SatPerBTC converts between satoshis and BTC.
	SatPerBTC = sdkmath.NewInt(100_000_000)

	maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

// Rpow is x^n in base b with half-up rounding at every step, exactly MakerDAO jug's
// rpow (exponentiation by squaring). x and b are RAY values in practice.
func Rpow(x sdkmath.Int, n uint64, b sdkmath.Int) sdkmath.Int {
	X, B := x.BigInt(), b.BigInt()
	if X.Sign() == 0 {
		if n == 0 {
			return b
		}
		return sdkmath.ZeroInt()
	}
	half := new(big.Int).Quo(B, big.NewInt(2))
	z := new(big.Int)
	if n%2 == 0 {
		z.Set(B)
	} else {
		z.Set(X)
	}
	xx := new(big.Int).Set(X)
	for n /= 2; n > 0; n /= 2 {
		xx.Mul(xx, xx)
		xx.Add(xx, half)
		xx.Quo(xx, B)
		if n%2 == 1 {
			z.Mul(z, xx)
			z.Add(z, half)
			z.Quo(z, B)
		}
	}
	return sdkmath.NewIntFromBigInt(z)
}

// ceilDiv is ceil(a / b) for a >= 0, b > 0.
func ceilDiv(a, b sdkmath.Int) sdkmath.Int {
	q := a.Quo(b)
	if !a.Mod(b).IsZero() {
		q = q.AddRaw(1)
	}
	return q
}

// DebtOf is ceil(art × rate / RAY) in ukusd (design §7.3).
func DebtOf(art, rate sdkmath.Int) sdkmath.Int { return ceilDiv(art.Mul(rate), RAY) }

// ArtForMint is ceil(d × RAY / rate): normalized debt added by minting d.
func ArtForMint(d, rate sdkmath.Int) sdkmath.Int { return ceilDiv(d.Mul(RAY), rate) }

// ArtForRepay is floor(r × RAY / rate): normalized debt removed by repaying r.
func ArtForRepay(r, rate sdkmath.Int) sdkmath.Int { return r.Mul(RAY).Quo(rate) }

// Ratio is art × WAD / collateralSat (floor); maximal when collateral is zero (§8.1).
func Ratio(art, collateralSat sdkmath.Int) *big.Int {
	if !collateralSat.IsPositive() {
		return new(big.Int).Set(maxUint256)
	}
	r := new(big.Int).Quo(art.Mul(WAD).BigInt(), collateralSat.BigInt())
	if r.Cmp(maxUint256) > 0 {
		r.Set(maxUint256)
	}
	return r
}

// LiqKey is the 32-byte big-endian (MaxUint256 − ratio) part of the §8.1 index key.
// Ascending order yields the highest ratio (worst CR) first.
func LiqKey(art, collateralSat sdkmath.Int) []byte {
	inv := new(big.Int).Sub(maxUint256, Ratio(art, collateralSat))
	out := make([]byte, 32)
	inv.FillBytes(out)
	return out
}

// IsLiquidatable is the exact §8.1 test at price P (micro-USD per BTC):
// collSat × P × 10^4 < LR_bps × debt × 10^8.
func IsLiquidatable(collateralSat, debt, price sdkmath.Int, lrBps uint32) bool {
	if !debt.IsPositive() {
		return false
	}
	lhs := collateralSat.Mul(price).MulRaw(10_000)
	rhs := debt.MulRaw(int64(lrBps)).Mul(SatPerBTC)
	return lhs.LT(rhs)
}

// MeetsMCR is the mint / withdrawal check at price P: collSat × P × 10^4 ≥
// MCR_bps × debt × 10^8 (debt 0 always passes).
func MeetsMCR(collateralSat, debt, price sdkmath.Int, mcrBps uint32) bool {
	if !debt.IsPositive() {
		return true
	}
	lhs := collateralSat.Mul(price).MulRaw(10_000)
	rhs := debt.MulRaw(int64(mcrBps)).Mul(SatPerBTC)
	return lhs.GTE(rhs)
}

// SeizeSat is design §8.4 pool step 1: min(collateral, ceil(D × (10,000 + penalty) ×
// 10^8 ÷ (10,000 × P))).
func SeizeSat(debt, price, collateralSat sdkmath.Int, penaltyBps uint32) sdkmath.Int {
	num := debt.MulRaw(int64(10_000 + penaltyBps)).Mul(SatPerBTC)
	s := ceilDiv(num, price.MulRaw(10_000))
	return sdkmath.MinInt(s, collateralSat)
}
