package app_test

import (
	"testing"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/testutil/apptest"
	admintypes "github.com/OWNER/keystone/x/admin/types"
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
