package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/x/stablecoin/types"
)

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.Params)
		ok     bool
	}{
		{"defaults", func(*types.Params) {}, true},
		{"sin_pause_bps 0", func(p *types.Params) { p.SinPauseBps = 0 }, false},
		{"sin_pause_bps 10001", func(p *types.Params) { p.SinPauseBps = 10_001 }, false},
		{"sin_pause_blocks 0", func(p *types.Params) { p.SinPauseBlocks = 0 }, false},
		{"sin_pause_blocks too high", func(p *types.Params) { p.SinPauseBlocks = 1_000_001 }, false},
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
	require.Equal(t, types.Params{SinPauseBps: 500, SinPauseBlocks: 1000, StakerShareBps: 2000}, types.DefaultParams())
}
