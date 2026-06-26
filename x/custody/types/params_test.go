package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/custody/types"
)

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.Params)
		ok     bool
	}{
		{"defaults", func(*types.Params) {}, true},
		{"cap 100 BTC", func(p *types.Params) { p.TreasuryCapSat = types.MaxTreasuryCapSat }, true},
		{"cap above 100 BTC", func(p *types.Params) { p.TreasuryCapSat = types.MaxTreasuryCapSat.AddRaw(1) }, false},
		{"cap negative", func(p *types.Params) { p.TreasuryCapSat = sdkmath.NewInt(-1) }, false},
		{"cap unset", func(p *types.Params) { p.TreasuryCapSat = sdkmath.Int{} }, false},
		{"membership delay 99", func(p *types.Params) { p.MembershipDelayBlocks = 99 }, false},
		{"membership delay 100 (localnet)", func(p *types.Params) { p.MembershipDelayBlocks = 100 }, true},
		{"confirm depth 0", func(p *types.Params) { p.ConfirmDepth = 0 }, false},
		{"confirm depth 1 (localnet)", func(p *types.Params) { p.ConfirmDepth = 1 }, true},
		{"nonce pool 0", func(p *types.Params) { p.NoncePool = 0 }, false},
		{"session deadline 0", func(p *types.Params) { p.SessionDeadline = 0 }, false},
		{"max inputs 0", func(p *types.Params) { p.MaxInputs = 0 }, false},
		{"batch min requests 0", func(p *types.Params) { p.BatchMinRequests = 0 }, false},
		{"batch max wait 0", func(p *types.Params) { p.BatchMaxWait = 0 }, false},
		{"feerate min 0", func(p *types.Params) { p.FeerateMin = 0 }, false},
		{"feerate min above max", func(p *types.Params) { p.FeerateMin = p.FeerateMax + 1 }, false},
		{"feerate max too high", func(p *types.Params) { p.FeerateMax = 10_001 }, false},
		{"rbf after 0", func(p *types.Params) { p.RbfAfterBtcBlocks = 0 }, false},
		{"min withdraw below dust limit", func(p *types.Params) { p.MinWithdrawSat = sdkmath.NewInt(329) }, false},
		{"emergency rekey on", func(p *types.Params) { p.EmergencyRekey = true }, true},
		{"network regtest", func(p *types.Params) { p.BitcoinNetwork = types.NetworkRegtest }, true},
		{"network mainnet", func(p *types.Params) { p.BitcoinNetwork = "mainnet" }, false},
		{"network empty", func(p *types.Params) { p.BitcoinNetwork = "" }, false},
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
	require.Equal(t, "1000000000", p.TreasuryCapSat.String()) // 10 BTC
	require.Equal(t, uint64(28800), p.MembershipDelayBlocks)
	require.Equal(t, uint32(6), p.ConfirmDepth)
	require.Equal(t, uint32(8), p.NoncePool)
	require.Equal(t, uint32(5), p.SessionDeadline)
	require.Equal(t, uint32(8), p.MaxInputs)
	require.Equal(t, uint32(16), p.BatchMinRequests)
	require.Equal(t, uint32(20), p.BatchMaxWait)
	require.Equal(t, uint64(1), p.FeerateMin)
	require.Equal(t, uint64(200), p.FeerateMax)
	require.Equal(t, uint32(6), p.RbfAfterBtcBlocks)
	require.Equal(t, "10000", p.MinWithdrawSat.String())
	require.False(t, p.EmergencyRekey)
	require.Equal(t, types.NetworkSignet, p.BitcoinNetwork)
}
