package protocol_test

import (
	"bytes"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol"
)

// passThroughPrepare stands in for the SDK default handler with a NoOp mempool:
// mempool order, within MaxTxBytes.
func passThroughPrepare(_ sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	var out [][]byte
	var size int64
	for _, tx := range req.Txs {
		if size+int64(len(tx)) > req.MaxTxBytes {
			break
		}
		size += int64(len(tx))
		out = append(out, tx)
	}
	return &abci.ResponsePrepareProposal{Txs: out}, nil
}

func acceptAll(sdk.Context, *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
}

type fixture struct {
	h    *protocol.ProposalHandler
	ext  abci.ExtendedCommitInfo
	last abci.CommitInfo
	keys int
}

// newFixture prepares height 11 with VEs from height 10 signed by 4 validators.
func newFixture(t *testing.T) fixture {
	keys := testKeys(4)
	ext, last := commitFor(t, keys, 10, 0, protocol.MinimalVE(10))
	return fixture{
		h:    protocol.NewProposalHandler(log.NewNopLogger(), storeFor(keys), passThroughPrepare, acceptAll),
		ext:  ext,
		last: last,
		keys: 4,
	}
}

func (f fixture) prepare(t *testing.T, mempool [][]byte, maxBytes int64) [][]byte {
	t.Helper()
	res, err := f.h.PrepareProposal(testCtx(), &abci.RequestPrepareProposal{
		Height: 11, Txs: mempool, MaxTxBytes: maxBytes, LocalLastCommit: f.ext,
	})
	require.NoError(t, err)
	return res.Txs
}

func (f fixture) process(t *testing.T, txs [][]byte, last abci.CommitInfo) abci.ResponseProcessProposal_ProposalStatus {
	t.Helper()
	res, err := f.h.ProcessProposal(testCtx(), &abci.RequestProcessProposal{Height: 11, Txs: txs, ProposedLastCommit: last})
	require.NoError(t, err)
	return res.Status
}

var (
	accept = abci.ResponseProcessProposal_ACCEPT
	reject = abci.ResponseProcessProposal_REJECT
)

func TestPrepareOutputPassesProcess(t *testing.T) {
	f := newFixture(t)
	user := [][]byte{[]byte("user-tx-1"), []byte("KSTN-smuggled"), []byte("user-tx-2")}
	txs := f.prepare(t, user, 1<<20)
	require.Len(t, txs, 3, "tx0 + two user txs; the magic mempool tx is dropped")
	require.True(t, protocol.HasMagic(txs[0]))
	require.Equal(t, []byte("user-tx-1"), txs[1])
	require.Equal(t, []byte("user-tx-2"), txs[2])
	require.Equal(t, accept, f.process(t, txs, f.last))
}

func TestPrepareRespectsMaxTxBytes(t *testing.T) {
	f := newFixture(t)
	tx0 := protocol.EncodeInjected(f.ext)
	user := [][]byte{bytes.Repeat([]byte{'a'}, 100), bytes.Repeat([]byte{'b'}, 100)}

	txs := f.prepare(t, user, int64(len(tx0))+150)
	require.Len(t, txs, 2, "tx0 plus the first user tx only")
	var size int
	for _, tx := range txs {
		size += len(tx)
	}
	require.LessOrEqual(t, size, len(tx0)+150)

	txs = f.prepare(t, user, int64(len(tx0)))
	require.Equal(t, [][]byte{tx0}, txs, "no room for user txs")
}

func TestProcessRejects(t *testing.T) {
	f := newFixture(t)
	good := f.prepare(t, [][]byte{[]byte("user")}, 1<<20)
	tx0 := good[0]

	twoBad := cloneExt(f.ext)
	twoBad.Votes[0].ExtensionSignature[0] ^= 1
	twoBad.Votes[1].ExtensionSignature[0] ^= 1
	oneBad := cloneExt(f.ext)
	oneBad.Votes[0].ExtensionSignature[0] ^= 1

	dropped := cloneExt(f.ext)
	dropped.Votes = dropped.Votes[1:]

	cases := []struct {
		name string
		txs  [][]byte
		want abci.ResponseProcessProposal_ProposalStatus
	}{
		{"valid", good, accept},
		{"missing tx0 (no txs)", nil, reject},
		{"missing tx0 (user tx first)", [][]byte{[]byte("user")}, reject},
		{"tx0 at index 1", [][]byte{[]byte("user"), tx0}, reject},
		{"extra magic tx", [][]byte{tx0, []byte("user"), []byte("KSTN\x01")}, reject},
		{"undecodable tx0", [][]byte{[]byte("KSTN\x01\xff\xff")}, reject},
		{"tampered vote list (vote dropped)", [][]byte{protocol.EncodeInjected(dropped)}, reject},
		{"verified power at 2/4", [][]byte{protocol.EncodeInjected(twoBad)}, reject},
		{"one bad VE still above 2/3", [][]byte{protocol.EncodeInjected(oneBad)}, accept},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, f.process(t, tc.txs, f.last))
		})
	}
}

func TestProcessRejectsCommitNotMatchingLastCommit(t *testing.T) {
	f := newFixture(t)
	txs := f.prepare(t, nil, 1<<20)
	other := abci.CommitInfo{Round: 1, Votes: f.last.Votes}
	require.Equal(t, reject, f.process(t, txs, other))
}

func TestBeforeVEsActive(t *testing.T) {
	f := newFixture(t)
	ctx := testCtx()
	// Height 1 (the enable height): no tx0 is proposed and a magic tx is rejected.
	res, err := f.h.PrepareProposal(ctx, &abci.RequestPrepareProposal{Height: 1, Txs: [][]byte{[]byte("u"), []byte("KSTNx")}, MaxTxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("u")}, res.Txs)

	pr, err := f.h.ProcessProposal(ctx, &abci.RequestProcessProposal{Height: 1, Txs: res.Txs})
	require.NoError(t, err)
	require.Equal(t, accept, pr.Status)

	pr, err = f.h.ProcessProposal(ctx, &abci.RequestProcessProposal{Height: 1, Txs: [][]byte{protocol.EncodeInjected(f.ext)}})
	require.NoError(t, err)
	require.Equal(t, reject, pr.Status)

	// VEs disabled entirely.
	off := ctx.WithConsensusParams(cmtproto.ConsensusParams{Abci: &cmtproto.ABCIParams{VoteExtensionsEnableHeight: 0}})
	require.False(t, protocol.VEActive(off, 100))
	require.False(t, protocol.VEActive(ctx.WithConsensusParams(cmtproto.ConsensusParams{}), 100))
	require.True(t, protocol.VEActive(ctx, 2))
	require.False(t, protocol.VEActive(ctx, 1))
}
