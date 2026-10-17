package app_test

import (
	"bytes"
	"testing"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/testutil/apptest"
	admintypes "github.com/OWNER/keystone/x/admin/types"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// TestTx0FromBlock2 runs blocks through Prepare/Process/ExtendVote/Finalize: block 1
// has no tx0; from block 2 txs[0] is tx0 and no other tx carries the magic.
func TestTx0FromBlock2(t *testing.T) {
	c := apptest.New(t, 4)
	for h := int64(1); h <= 6; h++ {
		b := c.NextBlock(nil)
		if h == 1 {
			for _, tx := range b.Txs {
				require.False(t, protocol.HasMagic(tx), "block 1 has no tx0")
			}
			continue
		}
		require.NotEmpty(t, b.Txs)
		ext, ok := protocol.DecodeInjected(b.Txs[0])
		require.True(t, ok, "height %d: txs[0] is tx0", h)
		require.Len(t, ext.Votes, 4)
		for _, tx := range b.Txs[1:] {
			require.False(t, protocol.HasMagic(tx))
		}
		require.Equal(t, "4", eventAttr(b.Finalize.Events, protocol.EventTypeProtocolVotes, protocol.AttributeKeyVerifiedPower))
	}
}

// TestUnequalPowerVotes: VE verification and the tx0 vote order follow stake.
func TestUnequalPowerVotes(t *testing.T) {
	c := apptest.NewWithPowers(t, []int64{5, 1, 3})
	var b apptest.Block
	for range 3 {
		b = c.NextBlock(nil)
	}
	ext, ok := protocol.DecodeInjected(b.Txs[0])
	require.True(t, ok)
	require.Len(t, ext.Votes, 3)
	require.EqualValues(t, 5, ext.Votes[0].Validator.Power, "votes sorted by power descending")
	require.Equal(t, "9", eventAttr(b.Finalize.Events, protocol.EventTypeProtocolVotes, protocol.AttributeKeyVerifiedPower))
}

func createValidatorMsg(t *testing.T, op sdk.AccAddress, cons ed25519.PrivKey, power int64) *stakingtypes.MsgCreateValidator {
	t.Helper()
	msg, err := stakingtypes.NewMsgCreateValidator(
		sdk.ValAddress(op).String(), &sdked25519.PubKey{Key: cons.PubKey().Bytes()},
		sdk.NewCoin(app.BondDenom, sdk.DefaultPowerReduction.MulRaw(power)),
		stakingtypes.Description{Moniker: "new"},
		stakingtypes.NewCommissionRates(sdkmath.LegacyNewDecWithPrec(5, 2), sdkmath.LegacyNewDecWithPrec(20, 2), sdkmath.LegacyNewDecWithPrec(1, 2)),
		sdkmath.OneInt(),
	)
	require.NoError(t, err)
	return msg
}

// TestAllowlistGatesValidators is the M7 replacement of M0 acceptance check 3: a
// non-allowlisted MsgCreateValidator is rejected by the ante handler; once the admin
// allowlists the operator, its validator joins the set two heights later and its VEs
// verify; removing it from the allowlist jails it.
func TestAllowlistGatesValidators(t *testing.T) {
	c := apptest.New(t, 1)
	c.NextBlock(nil)
	c.NextBlock(nil)

	newCons := ed25519.GenPrivKeyFromSecret([]byte("new-operator-cons"))
	op := c.Users[1]
	create := createValidatorMsg(t, op.Addr, newCons, 1)

	b := c.NextBlock([][]byte{c.SignTx(op.Key, create)})
	require.Len(t, b.Finalize.TxResults, 2) // tx0 + the user tx
	require.Equal(t, app.ErrNotAllowlisted.ABCICode(), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)
	require.Empty(t, b.Finalize.ValidatorUpdates)

	// Non-admin allowlisting: rejected.
	stranger := c.Users[0]
	add := &admintypes.MsgSetAllowlist{Admin: stranger.Addr.String(), Add: []admintypes.AllowInput{{Operator: op.Addr.String(), Moniker: "new"}}}
	b = c.NextBlock([][]byte{c.SignTx(stranger.Key, add)})
	require.Contains(t, b.Finalize.TxResults[1].Log, admintypes.ErrUnauthorized.Error())

	add.Admin = c.Admin.Addr.String()
	b = c.NextBlock([][]byte{c.SignTx(c.Admin.Key, add)})
	require.Equal(t, uint32(0), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)

	b = c.NextBlock([][]byte{c.SignTx(op.Key, create)})
	require.Equal(t, uint32(0), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)
	require.Len(t, b.Finalize.ValidatorUpdates, 1)
	require.EqualValues(t, 1, b.Finalize.ValidatorUpdates[0].Power)
	require.Equal(t, newCons.PubKey().Bytes(), b.Finalize.ValidatorUpdates[0].PubKey.GetEd25519())

	c.RegisterConsKey(newCons)
	for range 4 {
		b = c.NextBlock(nil)
	}
	ext, ok := protocol.DecodeInjected(b.Txs[0])
	require.True(t, ok)
	require.Len(t, ext.Votes, 2)
	require.Equal(t, "2", eventAttr(b.Finalize.Events, protocol.EventTypeProtocolVotes, protocol.AttributeKeyVerifiedPower))

	// Removal from the allowlist jails the validator: power 0 at the end of the block.
	rm := &admintypes.MsgSetAllowlist{Admin: c.Admin.Addr.String(), Remove: []string{op.Addr.String()}}
	b = c.NextBlock([][]byte{c.SignTx(c.Admin.Key, rm)})
	require.Equal(t, uint32(0), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)
	require.Len(t, b.Finalize.ValidatorUpdates, 1)
	require.Zero(t, b.Finalize.ValidatorUpdates[0].Power)
	v, err := c.App.StakingKeeper.GetValidator(c.Context(), sdk.ValAddress(op.Addr))
	require.NoError(t, err)
	require.True(t, v.IsJailed())
}

// TestKeyRotationRejected: MsgRotateConsPubKey never reaches x/staking (ADR-015).
func TestKeyRotationRejected(t *testing.T) {
	c := apptest.New(t, 1)
	c.NextBlock(nil)
	op := c.Operators[0]
	msg, err := stakingtypes.NewMsgRotateConsPubKey(sdk.ValAddress(op.Addr).String(),
		&sdked25519.PubKey{Key: ed25519.GenPrivKeyFromSecret([]byte("rotated")).PubKey().Bytes()})
	require.NoError(t, err)
	b := c.NextBlock([][]byte{c.SignTx(op.AccKey, msg)})
	require.Equal(t, app.ErrKeyRotationDisabled.ABCICode(), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)
}

func eventAttr(evs []abciEvent, typ, key string) string {
	for _, e := range evs {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return a.Value
			}
		}
	}
	return ""
}

// TestTheftSlashesWithStaking runs §9.12's op against the real x/staking and
// x/slashing keepers: every share holder of the epoch loses its stake (fraction 1),
// is jailed and tombstoned, and deposits pause.
func TestTheftSlashesWithStaking(t *testing.T) {
	c := apptest.NewWithPowers(t, []int64{3, 2, 1, 1})
	for range 3 {
		c.NextBlock(nil)
	}
	ctx := c.Context().WithBlockHeight(c.Height + 1).WithEventManager(sdk.NewEventManager())
	k := c.App.CustodyKeeper
	txid := bytes.Repeat([]byte{0x11}, 32)
	u := custodytypes.Utxo{Txid: txid, Vout: 0, AmountSat: 50_000_000, Epoch: c.Dealt.Epoch.Number, Status: custodytypes.UTXO_STATUS_AVAILABLE}
	require.NoError(t, k.Utxos.Set(ctx, collections.Join(txid, uint32(0)), u))

	batch := custodytypes.CustodyBatch{Spends: []vetypes.UnauthorizedSpend{{
		PrevTxid: txid, PrevVout: 0, SpendingTxid: bytes.Repeat([]byte{0xEE}, 32),
		Block: &vetypes.BtcBlockRef{Height: 200, Hash: bytes.Repeat([]byte{0xB2}, 32)},
	}}}
	require.NoError(t, k.ApplyOps(ctx, batch))

	for _, op := range c.Dealt.Operators {
		v, err := c.App.StakingKeeper.GetValidator(ctx, sdk.ValAddress(op))
		require.NoError(t, err)
		require.True(t, v.Tokens.IsZero(), "%s keeps %s", op, v.Tokens)
		require.True(t, v.IsJailed())
		cons, err := v.GetConsAddr()
		require.NoError(t, err)
		require.True(t, c.App.SlashingKeeper.IsTombstoned(ctx, cons))
	}
	paused, err := k.IsTheftPaused(ctx)
	require.NoError(t, err)
	require.True(t, paused)
	require.Equal(t, sdk.DefaultPowerReduction.MulRaw(7).String(),
		eventAttr(abciEvents(ctx.EventManager().Events()), custodytypes.EventTypeTheftSlashed, custodytypes.AttributeKeySlashed))
}

func abciEvents(evs sdk.Events) []abciEvent {
	out := make([]abciEvent, len(evs))
	for i, e := range evs {
		out[i] = abciEvent(e)
	}
	return out
}

// TestStakersEarnInflation (design §7.7, spec M7-T6): x/mint inflation in ukey in the
// fee collector reaches validators' outstanding rewards through x/distribution (the
// KUSD share takes the same path; x/stablecoin tests cover the split); gas stays zero.
func TestStakersEarnInflation(t *testing.T) {
	c := apptest.NewWithPowers(t, []int64{3, 1})
	c.NextBlock(nil)
	for range 4 {
		c.NextBlock(nil)
	}
	ctx := c.Context()
	for _, op := range c.Operators {
		r, err := c.App.DistrKeeper.GetValidatorOutstandingRewards(ctx, sdk.ValAddress(op.Addr))
		require.NoError(t, err)
		require.True(t, r.Rewards.AmountOf(app.BondDenom).IsPositive(), "%s earns ukey inflation", op.Addr)
	}

	// A user tx still pays no fee.
	from, to := c.Users[0], c.Users[1]
	b := c.NextBlock([][]byte{c.SignTx(from.Key, banktypes.NewMsgSend(from.Addr, to.Addr, sdk.NewCoins(sdk.NewInt64Coin(app.TestDenom, 1))))})
	require.Equal(t, uint32(0), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)
}
