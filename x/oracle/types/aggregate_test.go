package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/x/oracle/types"
)

const usd = 1_000_000 // micro-USD per USD

func rep(addr byte, power int64, priceUSD uint64) types.Report {
	return types.Report{ConsAddr: []byte{addr}, Power: power, Payload: &vetypes.OraclePayload{PriceMicroUsd: priceUSD * usd, NumSources: 3}}
}

// Required vector: powers 1,1,1,1; prices 99,990 / 100,020 / 100,050 / 150,000 →
// median 100,020; spot 100,020 with previous spot 99,800.
func TestMedianVector(t *testing.T) {
	reports := []types.Report{rep('A', 1, 100_020), rep('B', 1, 100_050), rep('C', 1, 99_990), rep('D', 1, 150_000)}
	agg := types.AggregatePrice(reports, 4, sdkmath.NewInt(99_800*usd), types.DefaultParams())
	require.True(t, agg.Updated)
	require.Equal(t, uint64(100_020*usd), agg.Median)
	require.Equal(t, sdkmath.NewInt(100_020*usd), agg.Spot)
	require.Equal(t, [][]byte{{'C'}, {'A'}, {'B'}, {'D'}}, agg.ReporterAddrs)
}

// Required vector: previous 100,000, every report 76,000, MaxStepBps 500 → spots
// 95,000 → 90,250 → 85,737.5 → 81,450.625 → 77,378.09375 → 76,000.
func TestClampWalkVector(t *testing.T) {
	p := types.DefaultParams()
	reports := []types.Report{rep(1, 1, 76_000), rep(2, 1, 76_000), rep(3, 1, 76_000), rep(4, 1, 76_000)}
	want := []int64{95_000_000_000, 90_250_000_000, 85_737_500_000, 81_450_625_000, 77_378_093_750, 76_000_000_000}
	prev := sdkmath.NewInt(100_000 * usd)
	for i, w := range want {
		agg := types.AggregatePrice(reports, 4, prev, p)
		require.True(t, agg.Updated)
		require.Equal(t, sdkmath.NewInt(w), agg.Spot, "step %d", i+1)
		prev = agg.Spot
	}
}

// Required vector: 4 operators, 2 report → no update (2×3 ≤ 4×2).
func TestQuorumVector(t *testing.T) {
	agg := types.AggregatePrice([]types.Report{rep(1, 1, 100_000), rep(2, 1, 100_000)}, 4, sdkmath.NewInt(100_000*usd), types.DefaultParams())
	require.False(t, agg.Updated)
	require.Equal(t, int64(2), agg.ReportedPower)

	// 3 of 4 is above 2/3.
	agg = types.AggregatePrice([]types.Report{rep(1, 1, 100_000), rep(2, 1, 100_000), rep(3, 1, 100_000)}, 4, sdkmath.NewInt(100_000*usd), types.DefaultParams())
	require.True(t, agg.Updated)
}

func TestAggregateRules(t *testing.T) {
	p := types.DefaultParams()

	// Bootstrap: no previous spot, no clamp.
	agg := types.AggregatePrice([]types.Report{rep(1, 1, 50_000)}, 1, sdkmath.ZeroInt(), p)
	require.Equal(t, sdkmath.NewInt(50_000*usd), agg.Spot)

	// Unusable payloads do not count toward quorum: missing, zero price, too few sources.
	few := rep(3, 1, 100_000)
	few.Payload.NumSources = 2
	reports := []types.Report{rep(1, 1, 100_000), {ConsAddr: []byte{2}, Power: 1}, few,
		{ConsAddr: []byte{4}, Power: 1, Payload: &vetypes.OraclePayload{NumSources: 3}}}
	agg = types.AggregatePrice(reports, 4, sdkmath.NewInt(100_000*usd), p)
	require.False(t, agg.Updated)
	require.Equal(t, int64(1), agg.ReportedPower)

	// Power-weighted: one operator with power 3 sets the median.
	agg = types.AggregatePrice([]types.Report{rep(1, 3, 100_000), rep(2, 1, 90_000), rep(3, 1, 110_000)}, 5, sdkmath.ZeroInt(), p)
	require.Equal(t, uint64(100_000*usd), agg.Median)

	// Ties in price break by address; the lower median is taken.
	agg = types.AggregatePrice([]types.Report{rep(9, 1, 100_000), rep(1, 1, 100_000), rep(5, 1, 101_000), rep(3, 1, 101_000)}, 4, sdkmath.ZeroInt(), p)
	require.Equal(t, uint64(100_000*usd), agg.Median)
	require.Equal(t, [][]byte{{1}, {9}, {3}, {5}}, agg.ReporterAddrs)

	// Upward clamp.
	agg = types.AggregatePrice([]types.Report{rep(1, 1, 200_000)}, 1, sdkmath.NewInt(100_000*usd), p)
	require.Equal(t, sdkmath.NewInt(105_000*usd), agg.Spot)
}

func TestIsOutlier(t *testing.T) {
	p := types.DefaultParams() // OutlierBps 200
	require.False(t, types.IsOutlier(102_000*usd, 100_000*usd, p))
	require.True(t, types.IsOutlier(102_001*usd, 100_000*usd, p))
	require.True(t, types.IsOutlier(97_999*usd, 100_000*usd, p))
	require.False(t, types.IsOutlier(98_000*usd, 100_000*usd, p))
}
