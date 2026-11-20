package multinode_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/app/protocol"
	"github.com/logicvoid0x/keystone/testutil/multinode"
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

func rewriteVotes(t *testing.T, tx0 []byte, f func(ext *abci.ExtendedCommitInfo)) []byte {
	t.Helper()
	ext, ok := protocol.DecodeInjected(tx0)
	require.True(t, ok)
	f(&ext)
	return protocol.EncodeInjected(ext)
}

func corruptSig(v *abci.ExtendedVoteInfo) {
	sig := append([]byte(nil), v.ExtensionSignature...)
	sig[0] ^= 0xFF
	v.ExtensionSignature = sig
}

func eventAttr(evs []abci.Event, typ, key string) string {
	for _, e := range evs {
		if e.Type == typ {
			for _, a := range e.Attributes {
				if a.Key == key {
					return a.Value
				}
			}
		}
	}
	return ""
}

// Spec M2-T4: the proposer of round 0 is replaced by a faulty one; every honest node's
// ProcessProposal decides, and after a rejection the next round (honest proposer)
// commits and the chain continues.
func TestFaultyProposer(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, net *multinode.Net, txs [][]byte) [][]byte
		accept bool
	}{
		{"no tx0", func(_ *testing.T, _ *multinode.Net, txs [][]byte) [][]byte { return txs[1:] }, false},
		{"tx0 at index 1", func(_ *testing.T, _ *multinode.Net, txs [][]byte) [][]byte {
			txs[0], txs[1] = txs[1], txs[0]
			return txs
		}, false},
		{"extra magic-prefixed tx (forged liquidation list)", func(_ *testing.T, _ *multinode.Net, txs [][]byte) [][]byte {
			return append(txs, append([]byte("KSTN\x02"), []byte("liquidate vault 1, 2, 3")...))
		}, false},
		{"votes dropped", func(t *testing.T, _ *multinode.Net, txs [][]byte) [][]byte {
			txs[0] = rewriteVotes(t, txs[0], func(e *abci.ExtendedCommitInfo) { e.Votes = e.Votes[:len(e.Votes)-1] })
			return txs
		}, false},
		{"votes reordered", func(t *testing.T, _ *multinode.Net, txs [][]byte) [][]byte {
			txs[0] = rewriteVotes(t, txs[0], func(e *abci.ExtendedCommitInfo) { e.Votes[0], e.Votes[1] = e.Votes[1], e.Votes[0] })
			return txs
		}, false},
		{"one VE signature corrupted (3/4 verified)", func(t *testing.T, _ *multinode.Net, txs [][]byte) [][]byte {
			txs[0] = rewriteVotes(t, txs[0], func(e *abci.ExtendedCommitInfo) { corruptSig(&e.Votes[2]) })
			return txs
		}, true},
		{"two VE signatures corrupted (2/4 verified)", func(t *testing.T, _ *multinode.Net, txs [][]byte) [][]byte {
			txs[0] = rewriteVotes(t, txs[0], func(e *abci.ExtendedCommitInfo) { corruptSig(&e.Votes[0]); corruptSig(&e.Votes[3]) })
			return txs
		}, false},
		{"tx0 copied from an earlier height", func(_ *testing.T, net *multinode.Net, txs [][]byte) [][]byte {
			txs[0] = net.Tx0[net.Height-1]
			return txs
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			net := multinode.New(t, 4)
			net.SetAllPrices(100_000)
			for i := 0; i < 4; i++ {
				net.Step(multinode.StepOpts{})
			}
			user := net.Users[0]
			r := net.Step(multinode.StepOpts{
				Txs:    [][]byte{net.SignTx(user.Key, &vaulttypes.MsgOpenVault{Owner: user.Addr.String()})},
				Mutate: func(n *multinode.Net, txs [][]byte) [][]byte { return tc.mutate(t, n, txs) },
			})
			first := r.Rounds[0]
			want := abci.ResponseProcessProposal_REJECT
			if tc.accept {
				want = abci.ResponseProcessProposal_ACCEPT
			}
			for i, s := range first.Statuses {
				require.Equal(t, want, s, "node %d on the faulty proposal", i)
			}
			if tc.accept {
				require.Len(t, r.Rounds, 1)
				require.Equal(t, "3", eventAttr(r.Finalize[0].Events, protocol.EventTypeProtocolVotes, protocol.AttributeKeyVerifiedPower),
					"the corrupted VE is ignored in aggregation")
			} else {
				require.Len(t, r.Rounds, 2, "round 1 (honest proposer) commits")
				require.NotEqual(t, first.Proposer, r.Accepted().Proposer)
				require.Equal(t, "4", eventAttr(r.Finalize[0].Events, protocol.EventTypeProtocolVotes, protocol.AttributeKeyVerifiedPower))
			}
			require.Equal(t, uint32(0), r.Finalize[0].TxResults[1].Code, "the user tx is executed")
			next := net.Step(multinode.StepOpts{})
			require.Len(t, next.Rounds, 1, "the chain continues")
		})
	}
}
