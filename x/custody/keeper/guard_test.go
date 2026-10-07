package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/custody/types"
)

// The removal guard (design §9.4, spec M5-T1, M7-T2): while an epoch holds funds, an
// allowlist removal must keep at least t of its share holders active.
func TestRemovalGuard(t *testing.T) {
	f := newSigning(t)
	g := f.k.RemovalGuard()
	ops := func(idx ...int) []sdk.AccAddress {
		out := make([]sdk.AccAddress, len(idx))
		for i, j := range idx {
			out[i] = f.signers[j].op
		}
		return out
	}
	// Unfunded: anything goes.
	require.NoError(t, g.AllowRemoval(f.ctx, ops(0, 1)))

	f.fund(t, 0x11, 50_000_000)
	require.NoError(t, g.AllowRemoval(f.ctx, ops(3)), "3 of 4 stay: t = 3")
	err := g.AllowRemoval(f.ctx, ops(2, 3))
	require.ErrorIs(t, err, types.ErrRemovalGuard, "removing 2 of 4 at once")
	// Removing someone who holds no share never hurts.
	require.NoError(t, g.AllowRemoval(f.ctx, []sdk.AccAddress{alice}))

	// A share holder already out of the active set counts as gone.
	f.fv.ops[3].Power = 0
	require.ErrorIs(t, g.AllowRemoval(f.ctx, ops(2)), types.ErrRemovalGuard)
}
