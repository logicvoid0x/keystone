package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/logicvoid0x/keystone/x/vault/types"
)

func mustInt(t *testing.T, s string) sdkmath.Int {
	t.Helper()
	v, ok := sdkmath.NewIntFromString(s)
	require.True(t, ok, s)
	return v
}

// Required test vectors (spec M1).
func TestRequiredVectors(t *testing.T) {
	fee := types.DefaultParams().StabilityFeePerSecond
	require.Equal(t, "1000000000627937192491029810", fee.String(), "per-second fee, 2%/yr")

	r30 := types.Rpow(fee, 2_592_000, types.RAY)
	require.Equal(t, "1001628938483711657286055762", r30.String(), "rpow 30 days")
	r1y := types.Rpow(fee, 31_536_000, types.RAY)
	require.Equal(t, "1019999999999999999972831879", r1y.String(), "rpow 1 year")

	art := sdkmath.NewInt(30_000_000_000)
	debt := types.DebtOf(art, r30)
	require.Equal(t, "30048868155", debt.String(), "debt after 30 days (ceil)")

	coll := sdkmath.NewInt(50_000_000)
	boundary := sdkmath.NewInt(78_127_057_203)
	require.False(t, types.IsLiquidatable(coll, debt, boundary, 13000), "not liquidatable at the boundary")
	require.True(t, types.IsLiquidatable(coll, debt, boundary.SubRaw(1), 13000), "liquidatable just below")

	seize := types.SeizeSat(debt, sdkmath.NewInt(76_000_000_000), coll, 1000)
	require.Equal(t, "43491783", seize.String(), "seizure")
	require.Equal(t, "6508217", coll.Sub(seize).String(), "owner keeps")
}

func TestRpow(t *testing.T) {
	ray := types.RAY
	require.Equal(t, ray, types.Rpow(sdkmath.NewInt(5), 0, ray), "x^0 = 1")
	require.True(t, types.Rpow(sdkmath.ZeroInt(), 3, ray).IsZero())
	require.Equal(t, ray, types.Rpow(sdkmath.ZeroInt(), 0, ray))
	two := ray.MulRaw(2)
	require.Equal(t, ray.MulRaw(1024), types.Rpow(two, 10, ray))
	require.Equal(t, ray, types.Rpow(ray, 123_456, ray))
	fee := types.DefaultParams().StabilityFeePerSecond
	require.Equal(t, fee, types.Rpow(fee, 1, ray))
}

func TestArtRounding(t *testing.T) {
	rate := mustInt(t, "1001628938483711657286055762")
	d := sdkmath.NewInt(1_000_000)
	art := types.ArtForMint(d, rate)
	require.True(t, types.DebtOf(art, rate).GTE(d), "minting d creates at least d of debt")
	rep := types.ArtForRepay(d, rate)
	require.True(t, rep.LTE(art), "repaying d removes at most the art minting d added")
	require.True(t, types.DebtOf(rep, rate).LTE(d.AddRaw(1)))
}

func TestLiqKeyOrdering(t *testing.T) {
	coll := sdkmath.NewInt(1_000)
	worse := types.LiqKey(sdkmath.NewInt(900), coll)
	better := types.LiqKey(sdkmath.NewInt(100), coll)
	require.Negative(t, bytes.Compare(worse, better), "higher ratio sorts first")
	require.Len(t, worse, 32)
	zeroColl := types.LiqKey(sdkmath.NewInt(1), sdkmath.ZeroInt())
	require.Equal(t, make([]byte, 32), zeroColl, "no collateral: maximal ratio, key zero")
}

func TestMeetsMCR(t *testing.T) {
	coll := sdkmath.NewInt(50_000_000)       // 0.5 BTC
	price := sdkmath.NewInt(100_000_000_000) // $100,000
	max := sdkmath.NewInt(33_333_333_333)    // floor(50,000 / 1.5) KUSD in ukusd
	require.True(t, types.MeetsMCR(coll, max, price, 15000))
	require.False(t, types.MeetsMCR(coll, max.AddRaw(1), price, 15000))
	require.True(t, types.MeetsMCR(sdkmath.ZeroInt(), sdkmath.ZeroInt(), price, 15000))
}
