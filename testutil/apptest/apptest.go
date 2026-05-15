// Package apptest runs a Keystone app in process for tests: genesis with N operators,
// InitChain, and blocks driven through FinalizeBlock and Commit. It is test support
// only; nothing in the state machine imports it.
package apptest

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app"
)

// ChainID is the chain id used by in-process tests.
const ChainID = "keystone-test-1"

// Operator is a test operator: its consensus key and its account key.
type Operator struct {
	ConsKey ed25519.PrivKey
	AccKey  *secp256k1.PrivKey
	Addr    sdk.AccAddress
}

// Account is a funded test key.
type Account struct {
	Key  *secp256k1.PrivKey
	Addr sdk.AccAddress
}

// Chain is an in-process single-app chain.
type Chain struct {
	T         *testing.T
	App       *app.App
	Operators []Operator // sorted by operator address
	Admin     Account
	Users     []Account
	Height    int64 // last committed height
	Time      time.Time

	// InitValidators are the Validator updates returned by InitChain.
	InitValidators []abci.ValidatorUpdate

	// Price is the price sidecar every Validator reports from (down until Set).
	Price *PriceFeed

	accNums map[string]uint64
	seqs    map[string]uint64
	rng     *rand.Rand

	// Consensus simulation for NextBlock (consensus.go).
	consKeys   map[string]ed25519.PrivKey // consensus address -> key
	valsets    map[int64][]Validator      // height -> Validator set
	lastExt    abci.ExtendedCommitInfo
	lastCommit abci.CommitInfo
}

// New starts a chain with n operators (power 1), an admin and two funded users, and
// runs InitChain. No block is committed yet.
func New(t *testing.T, n int) *Chain {
	t.Helper()
	c := &Chain{
		T:       t,
		Time:    time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		accNums: map[string]uint64{},
		seqs:    map[string]uint64{},
		rng:     rand.New(rand.NewSource(1)),
	}
	for i := 0; i < n; i++ {
		acc := secp256k1.GenPrivKeyFromSecret([]byte{'o', byte(i)})
		c.Operators = append(c.Operators, Operator{
			ConsKey: ed25519.GenPrivKeyFromSecret([]byte{'c', byte(i)}),
			AccKey:  acc,
			Addr:    sdk.AccAddress(acc.PubKey().Address()),
		})
	}
	sortOps(c.Operators)
	c.Admin = newAccount("admin")
	c.Users = []Account{newAccount("user0"), newAccount("user1")}

	c.App = app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(ChainID))
	c.Price = &PriceFeed{}
	c.App.SetPriceSource(c.Price)

	in := app.GenesisInput{Admin: c.Admin.Addr, FundAmount: sdkmath.NewInt(1_000_000_000)}
	for i, op := range c.Operators {
		in.Operators = append(in.Operators, app.GenesisOperator{
			Address: op.Addr, ConsPubKey: op.ConsKey.PubKey().Bytes(), Moniker: "op" + string(rune('0'+i)),
		})
	}
	all := []sdk.AccAddress{c.Admin.Addr}
	for _, u := range c.Users {
		all = append(all, u.Addr)
	}
	for _, op := range c.Operators {
		all = append(all, op.Addr)
	}
	in.Accounts = all
	gs, err := app.BuildGenesis(c.App.AppCodec(), c.App.DefaultGenesis(), in)
	require.NoError(t, err)
	stateBytes, err := json.Marshal(gs)
	require.NoError(t, err)

	cp := app.DefaultConsensusParams().ToProto()
	res, err := c.App.InitChain(&abci.RequestInitChain{
		ChainId:         ChainID,
		Time:            c.Time,
		InitialHeight:   1,
		AppStateBytes:   stateBytes,
		ConsensusParams: &cp,
	})
	require.NoError(t, err)
	c.InitValidators = res.Validators
	for i, a := range all {
		c.accNums[a.String()] = uint64(i)
	}
	c.consKeys = map[string]ed25519.PrivKey{}
	for _, op := range c.Operators {
		c.RegisterConsKey(op.ConsKey)
	}
	set := ApplyUpdates(nil, res.Validators)
	c.valsets = map[int64][]Validator{1: set, 2: set}
	return c
}

func newAccount(seed string) Account {
	k := secp256k1.GenPrivKeyFromSecret([]byte(seed))
	return Account{Key: k, Addr: sdk.AccAddress(k.PubKey().Address())}
}

func sortOps(ops []Operator) {
	for i := 1; i < len(ops); i++ {
		for j := i; j > 0 && string(ops[j].Addr) < string(ops[j-1].Addr); j-- {
			ops[j], ops[j-1] = ops[j-1], ops[j]
		}
	}
}

// SignTx signs msgs with key (zero fee) and returns the encoded tx. The sequence is
// tracked locally and advanced, so call it once per tx that will be delivered.
func (c *Chain) SignTx(key *secp256k1.PrivKey, msgs ...sdk.Msg) []byte {
	c.T.Helper()
	addr := sdk.AccAddress(key.PubKey().Address()).String()
	num, ok := c.accNums[addr]
	require.True(c.T, ok, "unknown account %s", addr)
	tx, err := simtestutil.GenSignedMockTx(c.rng, c.App.TxConfig(), msgs, sdk.NewCoins(), 1_000_000,
		ChainID, []uint64{num}, []uint64{c.seqs[addr]}, key)
	require.NoError(c.T, err)
	bz, err := c.App.TxConfig().TxEncoder()(tx)
	require.NoError(c.T, err)
	c.seqs[addr]++
	return bz
}

// Finalize runs FinalizeBlock for the next height with txs as given (no proposal
// handlers), then Commit.
func (c *Chain) Finalize(txs [][]byte, lastCommit abci.CommitInfo) *abci.ResponseFinalizeBlock {
	c.T.Helper()
	c.Height++
	c.Time = c.Time.Add(3 * time.Second)
	res, err := c.App.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:            c.Height,
		Time:              c.Time,
		Txs:               txs,
		DecidedLastCommit: lastCommit,
		ProposerAddress:   c.Operators[0].ConsKey.PubKey().Address(),
	})
	require.NoError(c.T, err)
	_, err = c.App.Commit()
	require.NoError(c.T, err)
	c.syncSequences()
	return res
}

// syncSequences reloads account sequences from committed state, so a rejected tx does
// not leave the local counter ahead of the chain.
func (c *Chain) syncSequences() {
	ctx := c.Context()
	for addr := range c.seqs {
		acc := c.App.AccountKeeper.GetAccount(ctx, sdk.MustAccAddressFromBech32(addr))
		if acc != nil {
			c.seqs[addr] = acc.GetSequence()
		}
	}
}

// Context returns a query context at the last committed height.
func (c *Chain) Context() sdk.Context {
	return c.App.NewContext(true)
}
