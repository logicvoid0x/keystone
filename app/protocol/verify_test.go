package protocol_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app/protocol"
)

// Our sign bytes must equal CometBFT's own.
func TestVoteExtSignBytesMatchCometBFT(t *testing.T) {
	ve := protocol.MinimalVE(12)
	got, err := protocol.VoteExtSignBytes(chainID, 12, 3, ve)
	require.NoError(t, err)
	want := cmttypes.VoteExtensionSignBytes(chainID, &cmtproto.Vote{Height: 12, Round: 3, Extension: ve})
	require.Equal(t, want, got)
}

func TestVerifyVotes(t *testing.T) {
	const h = 10
	keys := testKeys(4)
	vs := storeFor(keys)
	ve := protocol.MinimalVE(h)
	base, _ := commitFor(t, keys, h, 0, ve)

	badSig := func(e *abci.ExtendedCommitInfo, i int) { e.Votes[i].ExtensionSignature[0] ^= 0xff }
	cases := []struct {
		name     string
		mutate   func(e *abci.ExtendedCommitInfo)
		store    protocol.ValidatorStore
		verified int64
		total    int64
		quorum   bool
	}{
		{"all valid", func(*abci.ExtendedCommitInfo) {}, vs, 4, 4, true},
		{"one bad signature is ignored", func(e *abci.ExtendedCommitInfo) { badSig(e, 1) }, vs, 3, 4, true},
		{"two bad signatures", func(e *abci.ExtendedCommitInfo) { badSig(e, 0); badSig(e, 3) }, vs, 2, 4, false},
		{"absent votes count toward total only", func(e *abci.ExtendedCommitInfo) {
			e.Votes[2].BlockIdFlag = cmtproto.BlockIDFlagAbsent
			e.Votes[2].VoteExtension, e.Votes[2].ExtensionSignature = nil, nil
		}, vs, 3, 4, true},
		{"nil votes are not verified", func(e *abci.ExtendedCommitInfo) {
			e.Votes[2].BlockIdFlag = cmtproto.BlockIDFlagNil
			e.Votes[3].BlockIdFlag = cmtproto.BlockIDFlagNil
		}, vs, 2, 4, false},
		{"missing signature", func(e *abci.ExtendedCommitInfo) { e.Votes[0].ExtensionSignature = nil }, vs, 3, 4, true},
		{"unknown validator key", func(*abci.ExtendedCommitInfo) {}, storeFor(keys[:3]), 3, 4, true},
		{"wrong round in sign bytes", func(e *abci.ExtendedCommitInfo) { e.Round = 1 }, vs, 0, 4, false},
		{"signed but malformed VE is ignored", func(e *abci.ExtendedCommitInfo) {
			bad := protocol.MinimalVE(h + 1) // wrong height inside the VE
			sb := cmttypes.VoteExtensionSignBytes(chainID, &cmtproto.Vote{Height: h, Round: 0, Extension: bad})
			sig, err := keys[1].Sign(sb)
			require.NoError(t, err)
			e.Votes[1].VoteExtension, e.Votes[1].ExtensionSignature = bad, sig
		}, vs, 3, 4, true},
		{"key mapped to another address", func(*abci.ExtendedCommitInfo) {}, func() protocol.ValidatorStore {
			s := storeFor(keys)
			s[string(keys[0].PubKey().Address())] = ed25519.GenPrivKeyFromSecret([]byte("other")).PubKey().Bytes()
			return s
		}(), 3, 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := cloneExt(base)
			tc.mutate(&e)
			votes, verified, total := protocol.VerifyVotes(testCtx(), tc.store, chainID, h, e, protocol.VEVersion)
			require.Equal(t, tc.verified, verified)
			require.Equal(t, tc.total, total)
			require.Len(t, votes, int(tc.verified))
			require.Equal(t, tc.quorum, protocol.HasQuorum(verified, total))
		})
	}
}

func TestVerifyVotesWrongHeightOrChain(t *testing.T) {
	keys := testKeys(4)
	ext, _ := commitFor(t, keys, 10, 0, protocol.MinimalVE(10))
	_, verified, _ := protocol.VerifyVotes(testCtx(), storeFor(keys), chainID, 11, ext, protocol.VEVersion)
	require.Zero(t, verified, "signatures bind the height")
	_, verified, _ = protocol.VerifyVotes(testCtx(), storeFor(keys), "other-chain", 10, ext, protocol.VEVersion)
	require.Zero(t, verified, "signatures bind the chain id")
}

func TestHasQuorum(t *testing.T) {
	require.True(t, protocol.HasQuorum(3, 4))
	require.False(t, protocol.HasQuorum(2, 3)) // exactly 2/3 is not enough
	require.True(t, protocol.HasQuorum(3, 3))
	require.False(t, protocol.HasQuorum(0, 0))
}
