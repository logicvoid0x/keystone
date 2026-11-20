package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/admin/types"
)

func addr(i byte) string { return sdk.AccAddress(bytes.Repeat([]byte{i}, 20)).String() }

func TestGenesisValidate(t *testing.T) {
	good := func() types.GenesisState {
		return types.GenesisState{
			Params:    types.Params{Admin: addr(0xAD)},
			Allowlist: []types.AllowEntry{{Operator: addr(1), Moniker: "a"}, {Operator: addr(2), Moniker: "b"}},
		}
	}
	cases := []struct {
		name   string
		mutate func(*types.GenesisState)
		err    error
	}{
		{"valid", func(*types.GenesisState) {}, nil},
		{"bad admin", func(g *types.GenesisState) { g.Params.Admin = "x" }, types.ErrInvalidAddress},
		{"unsorted", func(g *types.GenesisState) { g.Allowlist[0], g.Allowlist[1] = g.Allowlist[1], g.Allowlist[0] }, types.ErrUnsorted},
		{"duplicate", func(g *types.GenesisState) { g.Allowlist[1] = g.Allowlist[0] }, types.ErrUnsorted},
		{"bad operator", func(g *types.GenesisState) { g.Allowlist[0].Operator = "x" }, types.ErrInvalidAddress},
		{"empty allowlist", func(g *types.GenesisState) { g.Allowlist = nil }, types.ErrEmptyAllowlist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := good()
			tc.mutate(&g)
			err := g.Validate()
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestGenesisAllowlistCovers(t *testing.T) {
	g := types.GenesisState{Allowlist: []types.AllowEntry{{Operator: addr(1)}}}
	one := sdk.AccAddress(bytes.Repeat([]byte{1}, 20))
	two := sdk.AccAddress(bytes.Repeat([]byte{2}, 20))
	require.True(t, g.Allowed(one))
	require.False(t, g.Allowed(two))
	require.NoError(t, g.ValidateAllowlistCovers([]sdk.AccAddress{one}))
	require.ErrorIs(t, g.ValidateAllowlistCovers([]sdk.AccAddress{one, two}), types.ErrNotAllowlisted)
}

func TestDefaultGenesisIsNotAChain(t *testing.T) {
	require.Error(t, types.DefaultGenesis().Validate())
}

func TestAllowAllGuard(t *testing.T) {
	require.NoError(t, types.AllowAll{}.AllowRemoval(t.Context(), nil))
}

func TestMsgSetAllowlistValidateBasic(t *testing.T) {
	cases := []struct {
		name string
		msg  types.MsgSetAllowlist
		err  error
	}{
		{"add", types.MsgSetAllowlist{Admin: addr(0xAD), Add: []types.AllowInput{{Operator: addr(1)}}}, nil},
		{"remove", types.MsgSetAllowlist{Admin: addr(0xAD), Remove: []string{addr(1), addr(2)}}, nil},
		{"empty", types.MsgSetAllowlist{Admin: addr(0xAD)}, types.ErrEmptyChange},
		{"bad admin", types.MsgSetAllowlist{Admin: "x", Remove: []string{addr(1)}}, types.ErrInvalidAddress},
		{"unsorted add", types.MsgSetAllowlist{Admin: addr(0xAD), Add: []types.AllowInput{{Operator: addr(2)}, {Operator: addr(1)}}}, types.ErrUnsorted},
		{"unsorted remove", types.MsgSetAllowlist{Admin: addr(0xAD), Remove: []string{addr(2), addr(1)}}, types.ErrUnsorted},
		{"overlap", types.MsgSetAllowlist{Admin: addr(0xAD), Add: []types.AllowInput{{Operator: addr(1)}}, Remove: []string{addr(1)}}, types.ErrOverlap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.msg.ValidateBasic()
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestMsgExecValidateBasic(t *testing.T) {
	require.ErrorIs(t, (&types.MsgExec{Admin: addr(0xAD)}).ValidateBasic(), types.ErrExecMsg)
	require.ErrorIs(t, (&types.MsgExec{Admin: "x"}).ValidateBasic(), types.ErrInvalidAddress)
}
