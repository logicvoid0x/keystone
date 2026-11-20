package protocol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol"
	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
	liqtypes "github.com/logicvoid0x/keystone/x/liquidation/types"
	oracletypes "github.com/logicvoid0x/keystone/x/oracle/types"
)

var testKey = storetypes.NewKVStoreKey("preblock_test")

// recorder implements every keeper the PreBlocker calls and logs the calls. failOn
// makes one step fail after writing to the store (the write must be discarded).
type recorder struct {
	calls  []string
	failOn string
	agg    oracletypes.PriceAggregate
	spot   sdkmath.Int
	stale  bool
	batch  liqtypes.LiquidationBatch
}

func (r *recorder) step(ctx context.Context, name string) error {
	r.calls = append(r.calls, name)
	sdk.UnwrapSDKContext(ctx).KVStore(testKey).Set([]byte(name), []byte{1})
	if r.failOn == name {
		return errors.New(name + " failed")
	}
	return nil
}

func (r *recorder) GetPriceState(context.Context) (oracletypes.PriceState, error) {
	return oracletypes.PriceState{SpotMicroUsd: sdkmath.NewInt(99_800_000_000)}, nil
}
func (r *recorder) GetParams(context.Context) (oracletypes.Params, error) {
	return oracletypes.DefaultParams(), nil
}
func (r *recorder) ApplyAggregate(ctx context.Context, agg oracletypes.PriceAggregate) error {
	r.agg = agg
	return r.step(ctx, "oracle")
}
func (r *recorder) LiquidationPrice(context.Context) (sdkmath.Int, bool, error) {
	return r.spot, !r.stale, nil
}
func (r *recorder) Drip(ctx context.Context, _ time.Time) error { return r.step(ctx, "drip") }
func (r *recorder) Select(ctx context.Context, price sdkmath.Int) (liqtypes.LiquidationBatch, error) {
	r.batch = liqtypes.LiquidationBatch{PriceMicroUsd: price}
	return r.batch, r.step(ctx, "select")
}
func (r *recorder) Execute(ctx context.Context, _ liqtypes.LiquidationBatch, _ sdkmath.Int) error {
	return r.step(ctx, "execute")
}
func (r *recorder) BuildOps(ctx context.Context, _ []custodytypes.Vote) (custodytypes.CustodyBatch, error) {
	return custodytypes.CustodyBatch{Anchors: make([]vetypes.BtcBlockRef, 1)}, r.step(ctx, "custody_build")
}
func (r *recorder) ApplyOps(ctx context.Context, _ custodytypes.CustodyBatch) error {
	return r.step(ctx, "custody")
}

func newPB(vs protocol.ValidatorStore, r *recorder) *protocol.PreBlocker {
	return protocol.NewPreBlocker(vs, protocol.Keepers{Oracle: r, Vault: r, Liquidation: r, Custody: r})
}

func pbCtx() sdk.Context { return testCtxWith(testKey) }

func lastEvent(ctx sdk.Context, typ string) (sdk.Event, bool) {
	evs := ctx.EventManager().Events()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return evs[i], true
		}
	}
	return sdk.Event{}, false
}

func attr(ev sdk.Event, key string) string {
	for _, a := range ev.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// signedPrices builds tx0 at vote height 10 where key i reports prices[i] USD.
func signedPrices(t *testing.T, prices []uint64) ([]byte, protocol.ValidatorStore) {
	keys := testKeys(len(prices))
	ext, _ := commitFor(t, keys, 10, 0, protocol.MinimalVE(10))
	for i, k := range keys {
		ve := vetypes.VoteExtension{Version: 1, Height: 10, Oracle: &vetypes.OraclePayload{PriceMicroUsd: prices[i] * 1_000_000, NumSources: 3}}
		bz, err := ve.Marshal()
		require.NoError(t, err)
		ext.Votes[i].VoteExtension = bz
		sig, err := k.Sign(signBytes(t, 10, 0, bz))
		require.NoError(t, err)
		ext.Votes[i].ExtensionSignature = sig
	}
	return protocol.EncodeInjected(ext), storeFor(keys)
}

func TestPreBlockStepOrderAndAggregate(t *testing.T) {
	tx0, vs := signedPrices(t, []uint64{100_020, 100_050, 99_990, 150_000})
	r := &recorder{spot: sdkmath.NewInt(100_020_000_000)}
	ctx := pbCtx()
	newPB(vs, r).PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{tx0}})
	require.Equal(t, []string{"oracle", "drip", "select", "execute", "custody_build", "custody"}, r.calls)
	// §6.3 example through the real aggregation: median 100,020, previous spot 99,800.
	require.True(t, r.agg.Updated)
	require.Equal(t, uint64(100_020_000_000), r.agg.Median)
	require.Equal(t, sdkmath.NewInt(100_020_000_000), r.agg.Spot)
	ev, ok := lastEvent(ctx, protocol.EventTypeProtocolVotes)
	require.True(t, ok)
	require.Equal(t, "4", attr(ev, protocol.AttributeKeyVerifiedPower))
}

func TestPreBlockStaleSkipsLiquidations(t *testing.T) {
	tx0, vs := signedPrices(t, []uint64{100_000, 100_000, 100_000, 100_000})
	r := &recorder{stale: true}
	newPB(vs, r).PreBlock(pbCtx(), &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{tx0}})
	require.Equal(t, []string{"oracle", "drip", "custody_build", "custody"}, r.calls)
}

// Totality: a failing step is discarded and reported; the following steps run.
func TestPreBlockStepFailureIsolated(t *testing.T) {
	tx0, vs := signedPrices(t, []uint64{100_000, 100_000, 100_000, 100_000})
	r := &recorder{spot: sdkmath.NewInt(100_000_000_000), failOn: "drip"}
	ctx := pbCtx()
	require.NotPanics(t, func() {
		newPB(vs, r).PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{tx0}})
	})
	require.Equal(t, []string{"oracle", "drip", "select", "execute", "custody_build", "custody"}, r.calls)
	ev, ok := lastEvent(ctx, protocol.EventTypeProtocolStepFailed)
	require.True(t, ok)
	require.Equal(t, protocol.StepDrip, attr(ev, protocol.AttributeKeyStep))
	store := ctx.KVStore(testKey)
	require.Nil(t, store.Get([]byte("drip")), "the failed step's writes are discarded")
	require.NotNil(t, store.Get([]byte("oracle")))
	require.NotNil(t, store.Get([]byte("custody")))

	// A failing Select skips Execute.
	r = &recorder{spot: sdkmath.NewInt(100_000_000_000), failOn: "select"}
	newPB(vs, r).PreBlock(pbCtx(), &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{tx0}})
	require.Equal(t, []string{"oracle", "drip", "select", "custody_build", "custody"}, r.calls)
}

func TestPreBlockTotality(t *testing.T) {
	r := &recorder{}
	pb := newPB(fakeStore{}, r)
	cases := map[string][][]byte{
		protocol.SkipMissingTx0: nil,
		protocol.SkipBadTx0:     {[]byte("not tx0")},
	}
	for reason, txs := range cases {
		t.Run(reason, func(t *testing.T) {
			ctx := pbCtx()
			require.NotPanics(t, func() { pb.PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 5, Txs: txs}) })
			ev, ok := lastEvent(ctx, protocol.EventTypeProtocolSkipped)
			require.True(t, ok)
			require.Equal(t, reason, attr(ev, protocol.AttributeKeyReason))
		})
	}
	require.Empty(t, r.calls)

	// Before VEs are active nothing runs.
	ctx := pbCtx()
	pb.PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 1})
	require.Empty(t, ctx.EventManager().Events())

	// A tx0 whose votes are all unknown still runs the steps (no price update).
	ext, _ := commitFor(t, testKeys(2), 4, 0, protocol.MinimalVE(4))
	pb.PreBlock(pbCtx(), &abci.RequestFinalizeBlock{Height: 5, Txs: [][]byte{protocol.EncodeInjected(ext)}})
	require.Contains(t, r.calls, "oracle")
	require.False(t, r.agg.Updated)
}

func TestPreBlockReportsUnverifiedVEs(t *testing.T) {
	keys := testKeys(4)
	ext, _ := commitFor(t, keys, 10, 0, protocol.MinimalVE(10))
	ext.Votes[2].ExtensionSignature[0] ^= 1
	var got [][]byte
	pb := newPB(storeFor(keys), &recorder{})
	pb.SetUnverifiedObserver(func(a []byte) { got = append(got, a) })
	pb.PreBlock(pbCtx(), &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{protocol.EncodeInjected(ext)}})
	require.Equal(t, [][]byte{ext.Votes[2].Validator.Address}, got)
}
