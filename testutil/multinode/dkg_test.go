package multinode_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/sidecar/dkg"
	"github.com/OWNER/keystone/testutil/multinode"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// Spec M4b-T4: a 4-node network with no dealer runs the epoch-1 DKG through
// transactions (the app hashes of all nodes are compared every height), every node's
// derived group key matches epoch_activated, and a withdrawal is then FROST-signed
// with the DKG key and passes the script engine.
func TestDKGThenWithdraw(t *testing.T) {
	net := multinode.NewWithOptions(t, 4, multinode.Options{DKG: true})
	net.SetAllPrices(100_000)
	k := net.Ref().App.CustodyKeeper
	var activated string
	for net.Height < 80 {
		r := net.Step(multinode.StepOpts{Txs: net.DKGTxs()})
		for _, tr := range r.Finalize[0].TxResults[min(1, len(r.Finalize[0].TxResults)):] {
			require.Zero(t, tr.Code, tr.Log)
		}
		for _, e := range r.Finalize[0].Events {
			if e.Type == custodytypes.EventTypeEpochActivated {
				for _, a := range e.Attributes {
					if a.Key == custodytypes.AttributeKeyGroupKey {
						activated = a.Value
					}
				}
			}
		}
		if activated != "" {
			break
		}
	}
	require.NotEmpty(t, activated, "epoch 1 ACTIVE within 80 heights")
	t.Logf("epoch 1 ACTIVE at height %d", net.Height)
	// Every participant derives and checks its share on the next steps.
	for range 2 {
		net.Step(multinode.StepOpts{Txs: net.DKGTxs()})
	}
	for _, nd := range net.Nodes {
		es := nd.Participant.Store.(*dkg.MemStore).Shares[1]
		require.Len(t, es.Shares, 1, "node %d has its share", nd.Index)
		require.Equal(t, activated, hex.EncodeToString(es.GroupKey), "node %d group key", nd.Index)
	}
	e, ok, err := k.GetActiveEpoch(net.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(3), e.T)

	// The M4a flow on the DKG key.
	u := net.Users[0]
	net.Step(multinode.StepOpts{Txs: [][]byte{net.SignTx(u.Key, &vaulttypes.MsgOpenVault{Owner: u.Addr.String()})}})
	net.Fund(1, 100_000_000)
	net.Step(multinode.StepOpts{Txs: [][]byte{withdrawTx(net, 20_000_000, 1)}})
	b := runToSigned(t, net, 1, 40)
	verifySigned(t, net, b)
}
