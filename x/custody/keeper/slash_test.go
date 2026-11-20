package keeper_test

import (
	"context"
	"errors"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/x/custody/types"
)

// fakeSlasher records slashes; fail makes the given operators fail.
type fakeSlasher struct {
	got  []sdk.AccAddress
	fail map[string]bool
}

func (s *fakeSlasher) SlashForTheft(_ context.Context, op sdk.AccAddress, height int64, fraction sdkmath.LegacyDec) (sdkmath.Int, error) {
	if s.fail[op.String()] {
		return sdkmath.ZeroInt(), errors.New("cannot slash")
	}
	s.got = append(s.got, op)
	return sdkmath.NewInt(100), nil
}

func spend(prev byte, vout uint32, by byte) vetypes.UnauthorizedSpend {
	return vetypes.UnauthorizedSpend{PrevTxid: h32(prev), PrevVout: vout, SpendingTxid: h32(by), Block: &vetypes.BtcBlockRef{Height: 120, Hash: h32(0xB2)}}
}

func hasEvent(ctx sdk.Context, typ string) bool {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// Design §9.12 / spec M7-T5: an attested unauthorized spend moves the UTXO to the
// deficit, slashes every share holder of its epoch once, and pauses deposits.
func TestUnauthorizedSpendSlashesEpoch(t *testing.T) {
	f := newSigning(t)
	sl := &fakeSlasher{fail: map[string]bool{}}
	f.k.SetSlasher(sl)
	f.fund(t, 0x11, 30_000_000, 20_000_000)

	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 0, 0xEE)}}
	f.block(t)
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2}

	_, err := f.k.Utxos.Get(f.ctx, collections.Join(h32(0x11), uint32(0)))
	require.ErrorIs(t, err, collections.ErrNotFound)
	def, err := f.k.Deficit.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, i(30_000_000), def)
	require.Len(t, sl.got, 4, "every share holder of epoch 1")
	paused, err := f.k.IsTheftPaused(f.ctx)
	require.NoError(t, err)
	require.True(t, paused)
	at, err := f.k.SlashedEpochs.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, f.ctx.BlockHeight(), at)

	// A second spend of the same epoch removes that UTXO too but slashes nobody again.
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 1, 0xEF)}}
	f.block(t)
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2}
	require.Len(t, sl.got, 4)
	def, err = f.k.Deficit.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, i(50_000_000), def)

	// While paused, a new deposit is not credited as collateral: it is claimable.
	f.fund(t, 0x12, 1_000_000)
	rec, err := f.k.Deposits.Get(f.ctx, collections.Join(h32(0x12), uint32(0)))
	require.NoError(t, err)
	require.Zero(t, rec.CollateralSat)
	require.EqualValues(t, 1_000_000, rec.ClaimableSat)
}

// Totality (§5.9): a member that cannot be slashed is skipped with an event; the rest
// are slashed and the block goes on.
func TestUnauthorizedSpendSlashFailureIsSkipped(t *testing.T) {
	f := newSigning(t)
	sl := &fakeSlasher{fail: map[string]bool{f.signers[1].op.String(): true}}
	f.k.SetSlasher(sl)
	f.fund(t, 0x11, 30_000_000)
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 0, 0xEE)}}
	f.block(t)
	require.Len(t, sl.got, 3)
	require.True(t, hasEvent(f.ctx, types.EventTypeSlashSkipped))
	require.True(t, hasEvent(f.ctx, types.EventTypeTheftSlashed))
}

// A spend by a Keystone batch transaction, or of an unknown outpoint, is not theft.
func TestUnauthorizedSpendIgnoredCases(t *testing.T) {
	f := newSigning(t)
	sl := &fakeSlasher{fail: map[string]bool{}}
	f.k.SetSlasher(sl)
	f.fund(t, 0x11, 30_000_000)
	require.NoError(t, f.k.TxBatch.Set(f.ctx, h32(0xEE), 1))
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 0, 0xEE), spend(0x77, 0, 0xEF)}}
	f.block(t)
	require.Empty(t, sl.got)
	require.True(t, hasEvent(f.ctx, types.EventTypeOpSkipped))
	paused, err := f.k.IsTheftPaused(f.ctx)
	require.NoError(t, err)
	require.False(t, paused)
}

// Fewer than 2/3 of power attesting is not enough.
func TestUnauthorizedSpendNeedsQuorum(t *testing.T) {
	f := newSigning(t)
	sl := &fakeSlasher{fail: map[string]bool{}}
	f.k.SetSlasher(sl)
	f.fund(t, 0x11, 30_000_000)
	f.step(t, &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 0, 0xEE)}}, 0, 1)
	require.Empty(t, sl.got, "2 of 4 is not > 2/3")
	f.step(t, &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 0, 0xEE)}}, 2)
	require.Len(t, sl.got, 4, "the tally accumulates across blocks")
}

func TestClearTheftPause(t *testing.T) {
	f := newSigning(t)
	f.k.SetSlasher(&fakeSlasher{fail: map[string]bool{}})
	f.fund(t, 0x11, 30_000_000)
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(0x11, 0, 0xEE)}}
	f.block(t)

	require.ErrorIs(t, f.k.ClearTheftPause(f.ctx, &types.MsgClearTheftPause{Admin: stranger}), errNotAdmin)
	require.ErrorIs(t, f.k.ClearTheftPause(f.ctx, &types.MsgClearTheftPause{Admin: admin}), types.ErrTheftPaused,
		"the slashed epoch is still ACTIVE")

	// Once another epoch is ACTIVE the admin can clear it.
	require.NoError(t, f.k.ActiveEpoch.Set(f.ctx, 2))
	require.NoError(t, f.k.ClearTheftPause(f.ctx, &types.MsgClearTheftPause{Admin: admin}))
	paused, err := f.k.IsTheftPaused(f.ctx)
	require.NoError(t, err)
	require.False(t, paused)
}

func TestTheftPauseInvariant(t *testing.T) {
	f := newSigning(t)
	require.NoError(t, f.k.TheftPauseInvariant(f.ctx))
	require.NoError(t, f.k.SlashedEpochs.Set(f.ctx, 1, 5))
	require.Error(t, f.k.TheftPauseInvariant(f.ctx), "slashed active epoch without a pause")
	require.NoError(t, f.k.TheftPaused.Set(f.ctx, true))
	require.NoError(t, f.k.TheftPauseInvariant(f.ctx))
}

// Property (invariant 15 and coverage under theft): over random deposits, attested
// spends (theft or not), clear attempts and the switch to a later epoch, the theft
// pause holds for
// a slashed ACTIVE epoch and held + deficit never shrinks below what was deposited.
func TestTheftProperty(t *testing.T) {
	f := newSigning(t)
	f.k.SetSlasher(&fakeSlasher{fail: map[string]bool{}})
	rng := rand.New(rand.NewSource(9))
	type out struct {
		txid byte
		vout uint32
	}
	var outs []out
	e1, err := f.k.Epochs.Get(f.ctx, 1)
	require.NoError(t, err)
	e2 := e1
	e2.Number = 2
	require.NoError(t, f.k.Epochs.Set(f.ctx, 2, e2)) // a later epoch to switch to
	deposited := int64(0)
	next := byte(0x20)
	for step := range 200 {
		switch r := rng.Intn(10); {
		case r < 4 && next < 0xE0:
			sat := int64(1_000_000 + rng.Intn(9_000_000))
			f.fund(t, next, uint64(sat))
			outs = append(outs, out{next, 0})
			deposited += sat
			next++
		case r < 7 && len(outs) > 0:
			o := outs[rng.Intn(len(outs))]
			f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Spends: []vetypes.UnauthorizedSpend{spend(o.txid, o.vout, byte(0xE0+rng.Intn(16)))}}
			f.block(t)
			f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2}
		case r < 9:
			_ = f.k.ClearTheftPause(f.ctx, &types.MsgClearTheftPause{Admin: admin})
		default:
			require.NoError(t, f.k.ActiveEpoch.Set(f.ctx, 2)) // epochs only advance
		}
		require.NoError(t, f.k.TheftPauseInvariant(f.ctx), "step %d", step)
		held, err := f.k.UtxoSum(f.ctx)
		require.NoError(t, err)
		def, err := f.k.Deficit.Get(f.ctx)
		require.NoError(t, err)
		require.True(t, held.Add(def).GTE(i(deposited)), "step %d: held %s + deficit %s < deposited %d", step, held, def, deposited)
	}
}
