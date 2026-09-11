package multinode

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
	"github.com/OWNER/keystone/sidecar/dkg"
	"github.com/OWNER/keystone/sidecar/signer"
	"github.com/OWNER/keystone/sidecar/signer/noncestore"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// setupDKG gives every node custody keys in genesis (no epoch), a DKG participant with
// an in-memory store, and a signer reading the shares the participant derives.
func (net *Net) setupDKG(gs app.GenesisState) {
	t := net.T
	cdc := net.Ref().App.AppCodec()
	var cg custodytypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(gs[custodytypes.ModuleName], &cg))
	cg.Params.BitcoinNetwork = custodytypes.NetworkRegtest
	cg.Params.ConfirmDepth = 1
	cg.Params.MembershipDelayBlocks = 100
	cg.Params.RetireWatchBlocks = 20
	for _, nd := range net.Nodes {
		enc, err := secret.RandomScalar(nil)
		require.NoError(t, err)
		pub, err := frost.SerializePoint(frost.MulBase(&enc))
		require.NoError(t, err)
		hot := sdk.AccAddress(nd.HotKey.PubKey().Address())
		cg.CustodyKeys = append(cg.CustodyKeys, custodytypes.CustodyKey{Operator: nd.Addr.String(), EncPubkey: pub[:], HotKey: hot.String()})
		store := &dkg.MemStore{}
		nd.Participant = &dkg.Participant{Operator: nd.Addr, HotKey: hot.String(), EncKey: enc, Store: store}
		ns, err := noncestore.Open(noncestore.Path(filepath.Join(t.TempDir(), fmt.Sprintf("node%d", nd.Index))))
		require.NoError(t, err)
		t.Cleanup(func() { _ = ns.Close() })
		nd.Signer = signer.New(signer.Config{View: nd.App.FrostView(), Store: ns, Shares: dkgShares{store}})
		nd.App.SetFrostSource(nodeFrost{nd})
	}
	slices.SortFunc(cg.CustodyKeys, func(a, b custodytypes.CustodyKey) int {
		return bytes.Compare(sdk.MustAccAddressFromBech32(a.Operator), sdk.MustAccAddressFromBech32(b.Operator))
	})
	bz, err := cdc.MarshalJSON(&cg)
	require.NoError(t, err)
	gs[custodytypes.ModuleName] = bz
}

// dkgShares serves the shares a DKG participant wrote.
type dkgShares struct{ store *dkg.MemStore }

func (d dkgShares) Epochs() ([]uint64, error) {
	var out []uint64
	for e := uint64(1); e <= 16; e++ {
		if ok, _ := d.store.HasShares(e); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func (d dkgShares) Share(epoch uint64) (uint32, frost.Scalar, error) {
	es := d.store.Shares[epoch]
	if len(es.Shares) != 1 {
		return 0, frost.Scalar{}, fmt.Errorf("no share for epoch %d", epoch)
	}
	s, err := frost.ParseScalar(es.Shares[0].Secret)
	return es.Shares[0].Index, s, err
}

// DKGTxs returns this height's DKG transactions: every node's participant steps on
// its own node's committed state, and its messages are signed by its hot key.
func (net *Net) DKGTxs() [][]byte {
	net.T.Helper()
	var txs [][]byte
	for _, nd := range net.Nodes {
		if nd.Participant == nil {
			continue
		}
		v, err := app.LoadDKGView(nd.App.NewContext(true), nd.App, nd.Addr)
		require.NoError(net.T, err)
		v.Height = net.Height
		msgs, err := nd.Participant.Step(v)
		require.NoError(net.T, err, "node %d participant", nd.Index)
		if len(msgs) > 0 {
			txs = append(txs, net.SignTx(nd.HotKey, msgs...))
		}
	}
	return txs
}
