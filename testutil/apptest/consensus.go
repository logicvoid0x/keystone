package apptest

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app/protocol"
)

// Validator is one entry of a simulated CometBFT validator set.
type Validator struct {
	Addr   []byte // consensus address
	PubKey []byte
	Power  int64
}

// RegisterConsKey lets NextBlock sign VEs for a Validator added after genesis.
func (c *Chain) RegisterConsKey(k ed25519.PrivKey) {
	c.consKeys[string(k.PubKey().Address())] = k
}

// ApplyUpdates returns set with the ABCI Validator updates applied (power 0 removes),
// sorted by power descending then address ascending, as CometBFT orders votes.
func ApplyUpdates(set []Validator, ups []abci.ValidatorUpdate) []Validator {
	out := slices.Clone(set)
	for _, u := range ups {
		pk := u.PubKey.GetEd25519()
		addr := ed25519.PubKey(pk).Address()
		i := slices.IndexFunc(out, func(v Validator) bool { return bytes.Equal(v.Addr, addr) })
		switch {
		case i >= 0 && u.Power == 0:
			out = slices.Delete(out, i, i+1)
		case i >= 0:
			out[i].Power = u.Power
		case u.Power > 0:
			out = append(out, Validator{Addr: addr, PubKey: pk, Power: u.Power})
		}
	}
	slices.SortFunc(out, func(a, b Validator) int {
		return cmp.Or(cmp.Compare(b.Power, a.Power), bytes.Compare(a.Addr, b.Addr))
	})
	return out
}

// Block is the result of NextBlock.
type Block struct {
	Height   int64
	Txs      [][]byte // the proposal, tx0 first when VEs are active
	Finalize *abci.ResponseFinalizeBlock
}

// NextBlock runs one height through the real handlers, as a single honest proposer
// with every registered Validator voting: PrepareProposal (with the previous height's
// extended commit), ProcessProposal (must accept), ExtendVote, VerifyVoteExtension,
// FinalizeBlock and Commit. It then signs every Validator's VE to form the extended
// commit for the next height.
func (c *Chain) NextBlock(mempool [][]byte) Block {
	c.T.Helper()
	h := c.Height + 1
	c.Time = c.Time.Add(3 * time.Second)
	set := c.valsets[h]
	require.NotEmpty(c.T, set, "no Validator set for height %d", h)
	proposer := set[0].Addr
	hash := bytes.Repeat([]byte{byte(h)}, 32)

	prep, err := c.App.PrepareProposal(&abci.RequestPrepareProposal{
		MaxTxBytes:      1 << 20,
		Txs:             mempool,
		LocalLastCommit: c.lastExt,
		Height:          h,
		Time:            c.Time,
		ProposerAddress: proposer,
	})
	require.NoError(c.T, err)

	proc, err := c.App.ProcessProposal(&abci.RequestProcessProposal{
		Txs:                prep.Txs,
		ProposedLastCommit: c.lastCommit,
		Hash:               hash,
		Height:             h,
		Time:               c.Time,
		ProposerAddress:    proposer,
	})
	require.NoError(c.T, err)
	require.Equal(c.T, abci.ResponseProcessProposal_ACCEPT, proc.Status, "ProcessProposal rejected height %d", h)

	_ = c.Watcher.Update(context.Background()) // fails before the first commit: no Bitcoin payload
	ext, err := c.App.ExtendVote(context.Background(), &abci.RequestExtendVote{
		Hash: hash, Height: h, Time: c.Time, Txs: prep.Txs, ProposedLastCommit: c.lastCommit, ProposerAddress: proposer,
	})
	require.NoError(c.T, err)
	ves := map[string][]byte{}
	for _, v := range set {
		ves[string(v.Addr)] = ext.VoteExtension
		if s, ok := c.Signers[string(v.Addr)]; ok {
			ves[string(v.Addr)] = WithFrost(c.T, ext.VoteExtension, h, s)
		}
		vr, err := c.App.VerifyVoteExtension(&abci.RequestVerifyVoteExtension{
			Hash: hash, ValidatorAddress: v.Addr, Height: h, VoteExtension: ves[string(v.Addr)],
		})
		require.NoError(c.T, err)
		require.Equal(c.T, abci.ResponseVerifyVoteExtension_ACCEPT, vr.Status)
	}

	fin, err := c.App.FinalizeBlock(&abci.RequestFinalizeBlock{
		Txs:               prep.Txs,
		DecidedLastCommit: c.lastCommit,
		Hash:              hash,
		Height:            h,
		Time:              c.Time,
		ProposerAddress:   proposer,
	})
	require.NoError(c.T, err)
	_, err = c.App.Commit()
	require.NoError(c.T, err)
	c.Height = h
	c.syncSequences()

	c.lastExt, c.lastCommit = c.signCommit(h, set, ves)
	c.valsets[h+2] = ApplyUpdates(c.valsets[h+1], fin.ValidatorUpdates)
	return Block{Height: h, Txs: prep.Txs, Finalize: fin}
}

// signCommit builds the extended commit and commit info for height h (round 0). Every
// Validator whose key is registered votes for the block with its VE bytes from ves.
func (c *Chain) signCommit(h int64, set []Validator, ves map[string][]byte) (abci.ExtendedCommitInfo, abci.CommitInfo) {
	var ext abci.ExtendedCommitInfo
	var last abci.CommitInfo
	for _, v := range set {
		val := abci.Validator{Address: v.Addr, Power: v.Power}
		key, ok := c.consKeys[string(v.Addr)]
		if !ok {
			ext.Votes = append(ext.Votes, abci.ExtendedVoteInfo{Validator: val, BlockIdFlag: cmtproto.BlockIDFlagAbsent})
			last.Votes = append(last.Votes, abci.VoteInfo{Validator: val, BlockIdFlag: cmtproto.BlockIDFlagAbsent})
			continue
		}
		ve := ves[string(v.Addr)]
		sb := cmttypes.VoteExtensionSignBytes(ChainID, &cmtproto.Vote{Height: h, Round: 0, Extension: ve})
		sig, err := key.Sign(sb)
		require.NoError(c.T, err)
		ext.Votes = append(ext.Votes, abci.ExtendedVoteInfo{
			Validator: val, VoteExtension: ve, ExtensionSignature: sig, BlockIdFlag: cmtproto.BlockIDFlagCommit,
		})
		last.Votes = append(last.Votes, abci.VoteInfo{Validator: val, BlockIdFlag: cmtproto.BlockIDFlagCommit})
	}
	return ext, last
}

// WithFrost returns ve (the encoded VE of height h) with s's FROST payload in it. A
// signer error leaves the payload out, as ExtendVote does (before the first commit the
// signer cannot read state).
func WithFrost(t testing.TB, ve []byte, h int64, s protocol.FrostSource) []byte {
	t.Helper()
	dec, err := protocol.DecodeVE(ve, h)
	require.NoError(t, err)
	p, err := s.Payload(h)
	if err != nil || p == nil {
		return ve
	}
	if len(p.Commitments) > 0 || len(p.Shares) > 0 || p.RevokeAllNonces {
		dec.Frost = p
	}
	protocol.Canonicalize(&dec)
	bz, err := dec.Marshal()
	require.NoError(t, err)
	return bz
}
