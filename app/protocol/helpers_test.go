package protocol_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"slices"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtprotocrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol"
)

const chainID = "keystone-protocol-test"

// fakeStore maps consensus addresses to ed25519 keys.
type fakeStore map[string][]byte

func (s fakeStore) GetPubKeyByConsAddr(_ context.Context, a sdk.ConsAddress) (cmtprotocrypto.PublicKey, error) {
	pk, ok := s[string(a)]
	if !ok {
		return cmtprotocrypto.PublicKey{}, errors.New("unknown validator")
	}
	return cmtprotocrypto.PublicKey{Sum: &cmtprotocrypto.PublicKey_Ed25519{Ed25519: pk}}, nil
}

// testKeys returns n ed25519 keys sorted by address (equal power, so CometBFT's
// vote order is address ascending).
func testKeys(n int) []ed25519.PrivKey {
	keys := make([]ed25519.PrivKey, n)
	for i := range keys {
		keys[i] = ed25519.GenPrivKeyFromSecret([]byte{'v', byte(i)})
	}
	slices.SortFunc(keys, func(a, b ed25519.PrivKey) int {
		return bytes.Compare(a.PubKey().Address(), b.PubKey().Address())
	})
	return keys
}

func storeFor(keys []ed25519.PrivKey) fakeStore {
	s := fakeStore{}
	for _, k := range keys {
		s[string(k.PubKey().Address())] = k.PubKey().Bytes()
	}
	return s
}

// commitFor signs ve at (height, round) with every key, using CometBFT's own sign
// bytes, and returns the extended commit and the matching commit info.
func commitFor(t *testing.T, keys []ed25519.PrivKey, height int64, round int32, ve []byte) (abci.ExtendedCommitInfo, abci.CommitInfo) {
	t.Helper()
	ext := abci.ExtendedCommitInfo{Round: round}
	last := abci.CommitInfo{Round: round}
	for _, k := range keys {
		sb := cmttypes.VoteExtensionSignBytes(chainID, &cmtproto.Vote{Height: height, Round: round, Extension: ve})
		sig, err := k.Sign(sb)
		require.NoError(t, err)
		val := abci.Validator{Address: k.PubKey().Address(), Power: 1}
		ext.Votes = append(ext.Votes, abci.ExtendedVoteInfo{
			Validator: val, VoteExtension: ve, ExtensionSignature: sig, BlockIdFlag: cmtproto.BlockIDFlagCommit,
		})
		last.Votes = append(last.Votes, abci.VoteInfo{Validator: val, BlockIdFlag: cmtproto.BlockIDFlagCommit})
	}
	slices.SortFunc(ext.Votes, func(a, b abci.ExtendedVoteInfo) int {
		return cmp.Or(cmp.Compare(b.Validator.Power, a.Validator.Power), bytes.Compare(a.Validator.Address, b.Validator.Address))
	})
	slices.SortFunc(last.Votes, func(a, b abci.VoteInfo) int {
		return cmp.Or(cmp.Compare(b.Validator.Power, a.Validator.Power), bytes.Compare(a.Validator.Address, b.Validator.Address))
	})
	return ext, last
}

// testCtx is a context with VEs enabled at height 1.
func testCtx() sdk.Context {
	ctx := testutil.DefaultContext(storetypes.NewKVStoreKey("protocol_test"), storetypes.NewTransientStoreKey("protocol_test_t"))
	return ctx.WithChainID(chainID).WithConsensusParams(cmtproto.ConsensusParams{
		Abci: &cmtproto.ABCIParams{VoteExtensionsEnableHeight: 1},
	}).WithEventManager(sdk.NewEventManager())
}

func cloneExt(e abci.ExtendedCommitInfo) abci.ExtendedCommitInfo {
	out := abci.ExtendedCommitInfo{Round: e.Round, Votes: make([]abci.ExtendedVoteInfo, len(e.Votes))}
	for i, v := range e.Votes {
		v.VoteExtension = slices.Clone(v.VoteExtension)
		v.ExtensionSignature = slices.Clone(v.ExtensionSignature)
		v.Validator.Address = slices.Clone(v.Validator.Address)
		out.Votes[i] = v
	}
	return out
}

