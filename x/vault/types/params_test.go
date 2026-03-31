package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/vault/types"
)

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.Params)
		ok     bool
	}{
		{"defaults", func(*types.Params) {}, true},
		{"mcr 10999", func(p *types.Params) { p.McrBps = 10_999 }, false},
		{"mcr 11000", func(p *types.Params) { p.McrBps, p.LrBps = 11_000, 10_500 }, true},
		{"mcr 30000", func(p *types.Params) { p.McrBps = 30_000 }, true},
		{"mcr 30001", func(p *types.Params) { p.McrBps = 30_001 }, false},
		{"mcr 0", func(p *types.Params) { p.McrBps = 0 }, false},
		{"lr equals mcr", func(p *types.Params) { p.LrBps = p.McrBps }, false},
		{"lr above mcr", func(p *types.Params) { p.LrBps = p.McrBps + 1 }, false},
		{"lr 100%", func(p *types.Params) { p.LrBps = 10_000 }, false},
		{"fee below RAY", func(p *types.Params) { p.StabilityFeePerSecond = types.RAY.SubRaw(1) }, false},
		{"fee RAY (0%)", func(p *types.Params) { p.StabilityFeePerSecond = types.RAY }, true},
		{"fee above 100%/yr", func(p *types.Params) { p.StabilityFeePerSecond = types.MaxStabilityFeePerSecond.AddRaw(1) }, false},
		{"fee unset", func(p *types.Params) { p.StabilityFeePerSecond = sdkmath.Int{} }, false},
		{"ceiling negative", func(p *types.Params) { p.GlobalDebtCeiling = sdkmath.NewInt(-1) }, false},
		{"ceiling too high", func(p *types.Params) { p.GlobalDebtCeiling = types.MaxGlobalDebtCeiling.AddRaw(1) }, false},
		{"dust above ceiling", func(p *types.Params) { p.Dust = p.GlobalDebtCeiling.AddRaw(1) }, false},
		{"dust negative", func(p *types.Params) { p.Dust = sdkmath.NewInt(-1) }, false},
		{"min deposit below dust limit", func(p *types.Params) { p.MinDepositSat = sdkmath.NewInt(329) }, false},
		{"min deposit above 1 BTC", func(p *types.Params) { p.MinDepositSat = sdkmath.NewInt(100_000_001) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := types.DefaultParams()
			tc.mutate(&p)
			err := p.Validate()
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, types.ErrInvalidParams)
			}
		})
	}
}

func TestDefaultsMatchDesign(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, uint32(15000), p.McrBps)
	require.Equal(t, uint32(13000), p.LrBps)
	require.Equal(t, "1000000000627937192491029810", p.StabilityFeePerSecond.String())
	require.Equal(t, "500000000000", p.GlobalDebtCeiling.String()) // 500,000 KUSD
	require.Equal(t, "100000000", p.Dust.String())                 // 100 KUSD
	require.Equal(t, "10000", p.MinDepositSat.String())
}
