package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/x/oracle/types"
)

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.Params)
		ok     bool
	}{
		{"defaults", func(*types.Params) {}, true},
		{"min_sources 0", func(p *types.Params) { p.MinSources = 0 }, false},
		{"min_sources 33", func(p *types.Params) { p.MinSources = 33 }, false},
		{"max_step_bps 99", func(p *types.Params) { p.MaxStepBps = 99 }, false},
		{"max_step_bps 100", func(p *types.Params) { p.MaxStepBps = 100 }, true},
		{"max_step_bps 2000", func(p *types.Params) { p.MaxStepBps = 2000 }, true},
		{"max_step_bps 2001", func(p *types.Params) { p.MaxStepBps = 2001 }, false},
		{"stale_blocks 0", func(p *types.Params) { p.StaleBlocks = 0 }, false},
		{"twap_blocks 0", func(p *types.Params) { p.TwapBlocks = 0 }, false},
		{"twap_blocks 10001", func(p *types.Params) { p.TwapBlocks = 10_001 }, false},
		{"volatile_bps 0", func(p *types.Params) { p.VolatileBps = 0 }, false},
		{"volatile_bps 10001", func(p *types.Params) { p.VolatileBps = 10_001 }, false},
		{"miss_window 0", func(p *types.Params) { p.MissWindow = 0 }, false},
		{"miss_alert 0", func(p *types.Params) { p.MissAlert = 0 }, false},
		{"miss_alert above window", func(p *types.Params) { p.MissAlert = p.MissWindow + 1 }, false},
		{"outlier_bps 0", func(p *types.Params) { p.OutlierBps = 0 }, false},
		{"outlier_bps 10001", func(p *types.Params) { p.OutlierBps = 10_001 }, false},
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
	require.Equal(t, types.Params{
		MinSources: 3, MaxStepBps: 500, StaleBlocks: 20, TwapBlocks: 100,
		VolatileBps: 1500, MissWindow: 1000, MissAlert: 200, OutlierBps: 200,
	}, types.DefaultParams())
}
