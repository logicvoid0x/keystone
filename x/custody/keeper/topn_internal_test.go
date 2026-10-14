package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func caddr(i byte) sdk.AccAddress { return sdk.AccAddress(bytes.Repeat([]byte{i}, 20)) }

func cand(i byte, power int64, incumbent bool) candidate {
	return candidate{addr: caddr(i), power: power, incumbent: incumbent}
}

func addrs(is ...byte) []sdk.AccAddress {
	out := make([]sdk.AccAddress, len(is))
	for j, i := range is {
		out[j] = caddr(i)
	}
	return out
}

// The §9.2 seat rule (M7-T4).
func TestSelectCustodySet(t *testing.T) {
	cases := []struct {
		name   string
		ins    []candidate
		outs   []candidate
		n      int
		margin uint32
		want   []sdk.AccAddress
	}{
		{
			name: "no epoch yet: top N by power, ties by address",
			outs: []candidate{cand(1, 10, false), cand(2, 30, false), cand(3, 20, false), cand(4, 20, false)},
			n:    3, want: addrs(2, 3, 4),
		},
		{
			name: "free seats go to the strongest outsiders",
			ins:  []candidate{cand(1, 5, true)},
			outs: []candidate{cand(2, 1, false), cand(3, 9, false)},
			n:    2, want: addrs(1, 3),
		},
		{
			name: "outsider within the margin does not displace",
			ins:  []candidate{cand(1, 100, true), cand(2, 100, true)},
			outs: []candidate{cand(3, 110, false)},
			n:    2, margin: 1000, want: addrs(1, 2),
		},
		{
			name: "outsider above the margin displaces the weakest incumbent",
			ins:  []candidate{cand(1, 100, true), cand(2, 90, true)},
			outs: []candidate{cand(3, 100, false)},
			n:    2, margin: 1000, want: addrs(1, 3),
		},
		{
			name: "several displacements, stopping at the first outsider that fails",
			ins:  []candidate{cand(1, 10, true), cand(2, 10, true), cand(3, 50, true)},
			outs: []candidate{cand(4, 40, false), cand(5, 30, false), cand(6, 10, false)},
			n:    3, margin: 1000, want: addrs(3, 4, 5),
		},
		{
			name: "more incumbents than seats keeps the strongest",
			ins:  []candidate{cand(1, 1, true), cand(2, 3, true), cand(3, 2, true)},
			n:    2, want: addrs(2, 3),
		},
		{
			name: "zero margin: strictly stronger outsider wins, equal does not",
			ins:  []candidate{cand(1, 10, true)},
			outs: []candidate{cand(2, 10, false)},
			n:    1, want: addrs(1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, selectCustodySet(tc.ins, tc.outs, tc.n, tc.margin))
		})
	}
}
