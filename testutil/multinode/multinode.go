// Package multinode simulates CometBFT for N Keystone app instances so determinism
// tests run in process (spec M2-T2). Each node has its own DB, consensus key and price
// sidecar. Per height: round-robin proposer, PrepareProposal on it, ProcessProposal on
// every node, ExtendVote / VerifyVoteExtension on every node, identical FinalizeBlock
// and Commit everywhere, then an app-hash comparison. Test support only.
package multinode

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/log/v2"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/sidecar/btcwatch"
	"github.com/OWNER/keystone/sidecar/signer"
	"github.com/OWNER/keystone/testutil/apptest"
	"github.com/OWNER/keystone/testutil/btcfake"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// ChainID is the chain id of every harness network.
const ChainID = "keystone-multinode-local-1"

// Node is one simulated operator.
type Node struct {
	Index   int
	App     *app.App
	ConsKey ed25519.PrivKey
	AccKey  *secp256k1.PrivKey
	Addr    sdk.AccAddress
	// Price is this node's own price sidecar.
	Price *apptest.PriceFeed
	// VEHook, if set, replaces this node's ExtendVote output (e.g. a timed-out VE).
	VEHook func(height int64, ve []byte) []byte
	// Watcher is this node's Bitcoin watcher over the shared fake chain. It is updated
	// before each ExtendVote unless SkipBitcoinUpdate is set (a lagging watcher).
	Watcher           *btcwatch.Watcher
	SkipBitcoinUpdate bool
	// Signer is this node's FROST signer (its nonce store in a temp dir). StopSigner
	// leaves the FROST payload out of its VEs (a stopped signer); FrostHook, if set,
	// rewrites the payload (e.g. an invalid share).
	Signer     *signer.Signer
	StopSigner bool
	FrostHook  func(p *vetypes.FrostPayload)
}

// nodeFrost is the FrostSource a node's App uses: its signer, unless stopped or hooked.
type nodeFrost struct{ n *Node }

func (f nodeFrost) Payload(h int64) (*vetypes.FrostPayload, error) {
	if f.n.StopSigner || f.n.Signer == nil {
		return nil, nil
	}
	p, err := f.n.Signer.Payload(h)
	if err == nil && f.n.FrostHook != nil {
		f.n.FrostHook(p)
	}
	return p, err
}

// ConsAddr is the node's consensus address.
func (n *Node) ConsAddr() []byte { return n.ConsKey.PubKey().Address() }

// Account is a funded workload key.
type Account struct {
	Key  *secp256k1.PrivKey
	Addr sdk.AccAddress
}

// Net is a simulated network.
type Net struct {
	T      testing.TB
	Nodes  []*Node
	Admin  Account
	Users  []Account
	Height int64
	Time   time.Time

	accNums    map[string]uint64
	seqs       map[string]uint64
	rng        *rand.Rand
	valsets    map[int64][]apptest.Validator
	lastExt    abci.ExtendedCommitInfo
	lastCommit abci.CommitInfo
	// Tx0 records the committed tx0 of each height (faulty-proposer tests reuse old ones).
	Tx0 map[int64][]byte
	// Bitcoin is the fake Bitcoin chain every node's watcher follows.
	Bitcoin *btcfake.Chain
}

// New starts an n-node network at genesis (no block committed).
func New(t testing.TB, n int) *Net {
	t.Helper()
	net := &Net{
		T: t, Time: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		accNums: map[string]uint64{}, seqs: map[string]uint64{}, rng: rand.New(rand.NewSource(7)),
		valsets: map[int64][]apptest.Validator{}, Tx0: map[int64][]byte{},
	}
	net.Bitcoin = btcfake.New(10)
	for i := 0; i < n; i++ {
		acc := secp256k1.GenPrivKeyFromSecret([]byte{'m', 'o', byte(i)})
		node := &Node{
			Index:   i,
			ConsKey: ed25519.GenPrivKeyFromSecret([]byte{'m', 'c', byte(i)}),
			AccKey:  acc,
			Addr:    sdk.AccAddress(acc.PubKey().Address()),
			Price:   &apptest.PriceFeed{},
		}
		node.App = app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(ChainID))
		node.App.SetPriceSource(node.Price)
		node.Watcher = btcwatch.New(net.Bitcoin, node.App.BitcoinView(), btcwatch.Config{Network: custodytypes.NetworkRegtest}, nil)
		node.App.SetBitcoinSource(node.Watcher)
		net.Nodes = append(net.Nodes, node)
	}
	adminKey := secp256k1.GenPrivKeyFromSecret([]byte("multinode-admin"))
	net.Admin = Account{adminKey, sdk.AccAddress(adminKey.PubKey().Address())}
	for i := 0; i < 3; i++ {
		k := secp256k1.GenPrivKeyFromSecret([]byte{'m', 'u', byte(i)})
		net.Users = append(net.Users, Account{k, sdk.AccAddress(k.PubKey().Address())})
	}

	ref := net.Nodes[0].App
	in := app.GenesisInput{Admin: net.Admin.Addr, FundAmount: sdkmath.NewInt(1_000_000_000)}
	accounts := []sdk.AccAddress{net.Admin.Addr}
	for _, u := range net.Users {
		accounts = append(accounts, u.Addr)
	}
	for _, nd := range net.Nodes {
		in.Operators = append(in.Operators, app.GenesisOperator{Address: nd.Addr, ConsPubKey: nd.ConsKey.PubKey().Bytes(), Moniker: fmt.Sprintf("node%d", nd.Index)})
		accounts = append(accounts, nd.Addr)
	}
	in.Accounts = accounts
	gs, err := app.BuildGenesis(ref.AppCodec(), ref.DefaultGenesis(), in)
	require.NoError(t, err)
	ops := make([]sdk.AccAddress, len(net.Nodes))
	for i, nd := range net.Nodes {
		ops[i] = nd.Addr
	}
	dealt, err := apptest.DealCustody(ref.AppCodec(), gs, ops)
	require.NoError(t, err)
	for _, nd := range net.Nodes {
		nd.Signer = apptest.NewSigner(t, dealt, nd.Addr, nd.App.FrostView(), filepath.Join(t.TempDir(), fmt.Sprintf("node%d", nd.Index)))
		nd.App.SetFrostSource(nodeFrost{nd})
	}
	state, err := json.Marshal(gs)
	require.NoError(t, err)
	cp := app.DefaultConsensusParams().ToProto()

	var vals []abci.ValidatorUpdate
	for _, nd := range net.Nodes {
		res, err := nd.App.InitChain(&abci.RequestInitChain{
			ChainId: ChainID, Time: net.Time, InitialHeight: 1, AppStateBytes: state, ConsensusParams: &cp,
		})
		require.NoError(t, err)
		vals = res.Validators
	}
	for i, a := range accounts {
		net.accNums[a.String()] = uint64(i)
	}
	set := apptest.ApplyUpdates(nil, vals)
	net.valsets[1], net.valsets[2] = set, set
	return net
}

// Ref is the reference node used for queries.
func (net *Net) Ref() *Node { return net.Nodes[0] }

// Context is a query context on the reference node.
func (net *Net) Context() sdk.Context { return net.Ref().App.NewContext(true) }

func (net *Net) nodeByCons(addr []byte) *Node {
	for _, nd := range net.Nodes {
		if bytes.Equal(nd.ConsAddr(), addr) {
			return nd
		}
	}
	return nil
}

// SignTx signs msgs with key (zero fee). Sequences resync from committed state after
// every height.
func (net *Net) SignTx(key *secp256k1.PrivKey, msgs ...sdk.Msg) []byte {
	net.T.Helper()
	addr := sdk.AccAddress(key.PubKey().Address()).String()
	num, ok := net.accNums[addr]
	require.True(net.T, ok, "unknown account %s", addr)
	cfg := net.Ref().App.TxConfig()
	tx, err := simtestutil.GenSignedMockTx(net.rng, cfg, msgs, sdk.NewCoins(), 2_000_000, ChainID,
		[]uint64{num}, []uint64{net.seqs[addr]}, key)
	require.NoError(net.T, err)
	bz, err := cfg.TxEncoder()(tx)
	require.NoError(net.T, err)
	net.seqs[addr]++
	return bz
}

func (net *Net) syncSequences() {
	ctx := net.Context()
	for addr := range net.seqs {
		if acc := net.Ref().App.AccountKeeper.GetAccount(ctx, sdk.MustAccAddressFromBech32(addr)); acc != nil {
			net.seqs[addr] = acc.GetSequence()
		}
	}
}

// StepOpts controls one height.
type StepOpts struct {
	Txs [][]byte
	// Mutate, if set, rewrites the proposer's PrepareProposal output in the rounds where
	// it returns true for apply (round 0 by default: MutateRounds = 1).
	Mutate       func(net *Net, txs [][]byte) [][]byte
	MutateRounds int
}

// RoundResult is the outcome of one proposal round.
type RoundResult struct {
	Round    int
	Proposer int
	Txs      [][]byte
	Statuses []abci.ResponseProcessProposal_ProposalStatus // per node
}

// StepResult is a committed height.
type StepResult struct {
	Height   int64
	Rounds   []RoundResult // every round, the last one accepted
	Txs      [][]byte
	Finalize []*abci.ResponseFinalizeBlock // per node
}

// Accepted is the committed round.
func (r StepResult) Accepted() RoundResult { return r.Rounds[len(r.Rounds)-1] }

func blockHash(h int64, round int) []byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(h))
	binary.BigEndian.PutUint64(b[8:], uint64(round))
	s := sha256.Sum256(b[:])
	return s[:]
}

// Step runs one height to commit. A round whose proposal any node rejects moves to the
// next proposer; nodes disagreeing on a proposal fail the test (non-determinism).
func (net *Net) Step(opts StepOpts) StepResult {
	t := net.T
	t.Helper()
	h := net.Height + 1
	net.Time = net.Time.Add(3 * time.Second)
	set := net.valsets[h]
	require.NotEmpty(t, set, "no validator set for height %d", h)
	mutateRounds := opts.MutateRounds
	if opts.Mutate != nil && mutateRounds == 0 {
		mutateRounds = 1
	}

	res := StepResult{Height: h}
	var txs [][]byte
	var round int
	var proposer *Node
	for round = 0; ; round++ {
		require.Less(t, round, 3*len(set), "height %d: no round accepted", h)
		proposer = net.nodeByCons(set[(int(h)+round)%len(set)].Addr)
		require.NotNil(t, proposer)
		prep, err := proposer.App.PrepareProposal(&abci.RequestPrepareProposal{
			MaxTxBytes: 1 << 20, Txs: opts.Txs, LocalLastCommit: net.lastExt,
			Height: h, Time: net.Time, ProposerAddress: proposer.ConsAddr(),
		})
		require.NoError(t, err)
		txs = prep.Txs
		if opts.Mutate != nil && round < mutateRounds {
			txs = opts.Mutate(net, slices.Clone(txs))
		}
		rr := RoundResult{Round: round, Proposer: proposer.Index, Txs: txs}
		accepted := 0
		for _, nd := range net.Nodes {
			pr, err := nd.App.ProcessProposal(&abci.RequestProcessProposal{
				Txs: txs, ProposedLastCommit: net.lastCommit, Hash: blockHash(h, round),
				Height: h, Time: net.Time, ProposerAddress: proposer.ConsAddr(),
			})
			require.NoError(t, err)
			rr.Statuses = append(rr.Statuses, pr.Status)
			if pr.Status == abci.ResponseProcessProposal_ACCEPT {
				accepted++
			}
		}
		res.Rounds = append(res.Rounds, rr)
		if accepted == len(net.Nodes) {
			break
		}
		require.Zero(t, accepted, "height %d round %d: nodes disagree on the proposal: %v", h, round, rr.Statuses)
	}
	hash := blockHash(h, round)

	// Watchers read the committed state of the previous height (as at ExtendVote).
	for _, nd := range net.Nodes {
		if !nd.SkipBitcoinUpdate {
			_ = nd.Watcher.Update(context.Background()) // fails before the first commit
		}
	}
	// Vote: every validator extends its vote; every node verifies every VE.
	ves := map[string][]byte{}
	for _, v := range set {
		nd := net.nodeByCons(v.Addr)
		ext, err := nd.App.ExtendVote(context.Background(), &abci.RequestExtendVote{
			Hash: hash, Height: h, Time: net.Time, Txs: txs, ProposedLastCommit: net.lastCommit, ProposerAddress: proposer.ConsAddr(),
		})
		require.NoError(t, err)
		ve := ext.VoteExtension
		if nd.VEHook != nil {
			ve = nd.VEHook(h, ve)
		}
		for _, other := range net.Nodes {
			vr, err := other.App.VerifyVoteExtension(&abci.RequestVerifyVoteExtension{Hash: hash, ValidatorAddress: v.Addr, Height: h, VoteExtension: ve})
			require.NoError(t, err)
			require.Equal(t, abci.ResponseVerifyVoteExtension_ACCEPT, vr.Status, "node %d rejects node %d's VE", other.Index, nd.Index)
		}
		ves[string(v.Addr)] = ve
	}

	for _, nd := range net.Nodes {
		fin, err := nd.App.FinalizeBlock(&abci.RequestFinalizeBlock{
			Txs: txs, DecidedLastCommit: net.lastCommit, Hash: hash, Height: h, Time: net.Time, ProposerAddress: proposer.ConsAddr(),
		})
		require.NoError(t, err)
		res.Finalize = append(res.Finalize, fin)
	}
	for _, nd := range net.Nodes {
		_, err := nd.App.Commit()
		require.NoError(t, err)
	}
	for i := 1; i < len(net.Nodes); i++ {
		if !bytes.Equal(res.Finalize[0].AppHash, res.Finalize[i].AppHash) {
			t.Fatalf("app hash mismatch at height %d: node0 %X, node%d %X\n%s", h,
				res.Finalize[0].AppHash, i, res.Finalize[i].AppHash, DiffStores(net.Nodes[0].App, net.Nodes[i].App))
		}
	}
	if len(txs) > 0 {
		net.Tx0[h] = txs[0]
	}
	net.Height = h
	res.Txs = txs
	net.syncSequences()
	net.lastExt, net.lastCommit = net.signCommit(h, int32(round), set, ves)
	net.valsets[h+2] = apptest.ApplyUpdates(net.valsets[h+1], res.Finalize[0].ValidatorUpdates)
	return res
}

// signCommit builds the extended commit for height h: each validator's own VE signed
// with its key (CometBFT VoteExtensionSignBytes), sorted power desc then address asc.
func (net *Net) signCommit(h int64, round int32, set []apptest.Validator, ves map[string][]byte) (abci.ExtendedCommitInfo, abci.CommitInfo) {
	ext := abci.ExtendedCommitInfo{Round: round}
	last := abci.CommitInfo{Round: round}
	for _, v := range set {
		nd := net.nodeByCons(v.Addr)
		val := abci.Validator{Address: v.Addr, Power: v.Power}
		ve := ves[string(v.Addr)]
		sig, err := nd.ConsKey.Sign(cmttypes.VoteExtensionSignBytes(ChainID, &cmtproto.Vote{Height: h, Round: round, Extension: ve}))
		require.NoError(net.T, err)
		ext.Votes = append(ext.Votes, abci.ExtendedVoteInfo{Validator: val, VoteExtension: ve, ExtensionSignature: sig, BlockIdFlag: cmtproto.BlockIDFlagCommit})
		last.Votes = append(last.Votes, abci.VoteInfo{Validator: val, BlockIdFlag: cmtproto.BlockIDFlagCommit})
	}
	byPower := func(a, b abci.Validator) int {
		return cmp.Or(cmp.Compare(b.Power, a.Power), bytes.Compare(a.Address, b.Address))
	}
	slices.SortFunc(ext.Votes, func(a, b abci.ExtendedVoteInfo) int { return byPower(a.Validator, b.Validator) })
	slices.SortFunc(last.Votes, func(a, b abci.VoteInfo) int { return byPower(a.Validator, b.Validator) })
	return ext, last
}

// LastExtendedCommit is the extended commit the next proposer will inject.
func (net *Net) LastExtendedCommit() abci.ExtendedCommitInfo { return net.lastExt }

// SetAllPrices sets every node's price feed to usd.
func (net *Net) SetAllPrices(usd uint64) {
	for _, nd := range net.Nodes {
		nd.Price.Set(usd)
	}
}

// LastCommit is the commit info of the last height (the next block's LastCommit).
func (net *Net) LastCommit() abci.CommitInfo { return net.lastCommit }

// DepositScript is vault id's deposit script on the reference node.
func (net *Net) DepositScript(id uint64) []byte {
	net.T.Helper()
	_, script, _, err := net.Ref().App.CustodyKeeper.DepositAddress(net.Context(), id)
	require.NoError(net.T, err)
	return script
}

// Fund pays sat to vault id on the fake Bitcoin chain, mines a block and steps until
// the deposit is accepted on the reference node (at most 4 heights).
func (net *Net) Fund(id uint64, sat int64) {
	net.T.Helper()
	txid := net.Bitcoin.Send(net.DepositScript(id), sat)
	net.Bitcoin.Mine(1)
	for i := 0; i < 4; i++ {
		net.Step(StepOpts{})
		if has, err := net.Ref().App.CustodyKeeper.Deposits.Has(net.Context(), collections.Join(txid[:], uint32(0))); err == nil && has {
			return
		}
	}
	net.T.Fatalf("deposit of %d sat to vault %d not accepted", sat, id)
}
