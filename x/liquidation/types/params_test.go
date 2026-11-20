package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/x/liquidation/types"
)

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.Params)
		ok     bool
	}{
		{"defaults", func(*types.Params) {}, true},
		{"penalty 0", func(p *types.Params) { p.PenaltyBps = 0 }, true},
		{"penalty 5001", func(p *types.Params) { p.PenaltyBps = 5001 }, false},
		{"max_per_block 0", func(p *types.Params) { p.MaxPerBlock = 0 }, false},
		{"max_per_block 1001", func(p *types.Params) { p.MaxPerBlock = 1001 }, false},
		{"auction_start below 100%", func(p *types.Params) { p.AuctionStartBps = 9999 }, false},
		{"auction_start 30001", func(p *types.Params) { p.AuctionStartBps = 30_001 }, false},
		{"auction_step 0", func(p *types.Params) { p.AuctionStepBps = 0 }, false},
		{"auction_reset 0", func(p *types.Params) { p.AuctionResetBps = 0 }, false},
		{"auction_reset 100%", func(p *types.Params) { p.AuctionResetBps = 10_000 }, false},
		{"auction_max_blocks 0", func(p *types.Params) { p.AuctionMaxBlocks = 0 }, false},
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
		PenaltyBps: 1000, MaxPerBlock: 50, AuctionStartBps: 12000,
		AuctionStepBps: 100, AuctionResetBps: 6000, AuctionMaxBlocks: 300,
	}, types.DefaultParams())
}
