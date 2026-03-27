package types_test

import (
	"bytes"
	"testing"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/poa/types"
)

func addr(i byte) string { return sdk.AccAddress(bytes.Repeat([]byte{i}, 20)).String() }

func pk(i byte) []byte { return ed25519.GenPrivKeyFromSecret([]byte{i}).PubKey().Bytes() }

func TestGenesisValidate(t *testing.T) {
	good := func() types.GenesisState {
		return types.GenesisState{
			Params: types.Params{Admin: addr(0xAD)},
			Operators: []types.Operator{
				{OperatorAddress: addr(1), ConsensusPubkey: pk(1), Power: 1},
				{OperatorAddress: addr(2), ConsensusPubkey: pk(2), Power: 0},
			},
		}
	}
	cases := []struct {
		name   string
		mutate func(*types.GenesisState)
		err    error
	}{
		{"valid", func(*types.GenesisState) {}, nil},
		{"bad admin", func(g *types.GenesisState) { g.Params.Admin = "x" }, types.ErrInvalidAddress},
		{"unsorted", func(g *types.GenesisState) { g.Operators[0], g.Operators[1] = g.Operators[1], g.Operators[0] }, types.ErrUnsorted},
		{"duplicate pubkey", func(g *types.GenesisState) { g.Operators[1].ConsensusPubkey = pk(1) }, types.ErrPubKeyUsed},
		{"power 2", func(g *types.GenesisState) { g.Operators[0].Power = 2 }, types.ErrInvalidPower},
		{"bad pubkey length", func(g *types.GenesisState) { g.Operators[0].ConsensusPubkey = []byte{1} }, types.ErrInvalidPubKey},
		{"no active operator", func(g *types.GenesisState) { g.Operators[0].Power = 0 }, types.ErrEmptyValidatorSet},
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

func TestDefaultGenesisIsNotAChain(t *testing.T) {
	require.Error(t, types.DefaultGenesis().Validate())
}

func TestAllowAllGuard(t *testing.T) {
	require.NoError(t, types.AllowAll{}.AllowSetValidators(nil, nil, nil)) //nolint:staticcheck // nil ctx is fine here
}
