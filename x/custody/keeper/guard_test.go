package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/x/custody/types"
	poatypes "github.com/OWNER/keystone/x/poa/types"
)

// The removal guard (design §9.4, spec M5-T1): while an epoch holds funds, a set
// change must keep at least t of its operators active.
func TestRemovalGuard(t *testing.T) {
	f := newSigning(t)
	g := f.k.RemovalGuard()
	set := func(off ...int) []poatypes.Operator {
		var out []poatypes.Operator
		for i, s := range f.signers {
			op := poatypes.Operator{OperatorAddress: s.op.String(), Power: 1}
			for _, o := range off {
				if o == i {
					op.Power = 0
				}
			}
			out = append(out, op)
		}
		return out
	}
	// Unfunded: anything goes.
	require.NoError(t, g.AllowSetValidators(f.ctx, set(), set(0, 1)))

	f.fund(t, 0x11, 50_000_000)
	require.NoError(t, g.AllowSetValidators(f.ctx, set(), set(3)), "3 of 4 stay: t = 3")
	err := g.AllowSetValidators(f.ctx, set(), set(2, 3))
	require.ErrorIs(t, err, types.ErrRemovalGuard, "removing 2 of 4 at once")
	// Adding an operator never hurts.
	more := append(set(), poatypes.Operator{OperatorAddress: alice.String(), Power: 1})
	require.NoError(t, g.AllowSetValidators(f.ctx, set(), more))
}
