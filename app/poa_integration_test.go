package app_test

import (
	"testing"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/testutil/apptest"
	poatypes "github.com/OWNER/keystone/x/poa/types"
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

// TestPoaSetValidators is M0 acceptance check 3: MsgSetValidators from the admin adding
// a second operator makes EndBlocker return a ValidatorUpdate with power 1; a non-admin
// signer is rejected.
func TestPoaSetValidators(t *testing.T) {
	c := apptest.New(t, 1)
	c.NextBlock(nil)
	c.NextBlock(nil)

	newCons := ed25519.GenPrivKeyFromSecret([]byte("new-operator-cons"))
	newAcc := secp256k1.GenPrivKeyFromSecret([]byte("new-operator-acc"))
	entry := poatypes.ValidatorEntry{
		OperatorAddress: sdk.AccAddress(newAcc.PubKey().Address()).String(),
		ConsensusPubkey: newCons.PubKey().Bytes(),
		Moniker:         "op-new",
		Power:           1,
	}

	// Non-admin signer: rejected, no validator update.
	stranger := c.Users[0]
	bad := c.SignTx(stranger.Key, &poatypes.MsgSetValidators{Admin: stranger.Addr.String(), Entries: []poatypes.ValidatorEntry{entry}})
	b := c.NextBlock([][]byte{bad})
	require.Len(t, b.Finalize.TxResults, 2) // tx0 + the user tx
	require.NotEqual(t, uint32(0), b.Finalize.TxResults[1].Code)
	require.Contains(t, b.Finalize.TxResults[1].Log, poatypes.ErrUnauthorized.Error())
	require.Empty(t, b.Finalize.ValidatorUpdates)

	// Admin: accepted, EndBlocker returns the new operator at power 1.
	good := c.SignTx(c.Admin.Key, &poatypes.MsgSetValidators{Admin: c.Admin.Addr.String(), Entries: []poatypes.ValidatorEntry{entry}})
	b = c.NextBlock([][]byte{good})
	require.Equal(t, uint32(0), b.Finalize.TxResults[1].Code, b.Finalize.TxResults[1].Log)
	require.Len(t, b.Finalize.ValidatorUpdates, 1)
	require.Equal(t, int64(1), b.Finalize.ValidatorUpdates[0].Power)
	require.Equal(t, newCons.PubKey().Bytes(), b.Finalize.ValidatorUpdates[0].PubKey.GetEd25519())
	require.Equal(t, entry.OperatorAddress, eventAttr(b.Finalize.TxResults[1].Events, poatypes.EventTypeValidatorSetUpdated, poatypes.AttributeKeyAdded))

	// The new operator joins the set two heights later and its VEs verify.
	c.RegisterConsKey(newCons)
	for i := 0; i < 4; i++ {
		b = c.NextBlock(nil)
	}
	ext, ok := protocol.DecodeInjected(b.Txs[0])
	require.True(t, ok)
	require.Len(t, ext.Votes, 2)
	require.Equal(t, "2", eventAttr(b.Finalize.Events, protocol.EventTypeProtocolVotes, protocol.AttributeKeyVerifiedPower))
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
