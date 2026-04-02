package protocol_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol"
)

func lastEvent(ctx sdk.Context) sdk.Event {
	evs := ctx.EventManager().Events()
	if len(evs) == 0 {
		return sdk.Event{}
	}
	return evs[len(evs)-1]
}

func attr(ev sdk.Event, key string) string {
	for _, a := range ev.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

func TestPreBlockRunsStepsWithAggregate(t *testing.T) {
	keys := testKeys(4)
	ext, _ := commitFor(t, keys, 10, 0, protocol.MinimalVE(10))
	ext.Votes[0].ExtensionSignature[0] ^= 1 // one bad VE

	var got []protocol.Aggregated
	step := func(_ sdk.Context, a protocol.Aggregated) { got = append(got, a) }
	pb := protocol.NewPreBlocker(storeFor(keys), step, step)

	ctx := testCtx()
	pb.PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{protocol.EncodeInjected(ext)}})
	require.Equal(t, []protocol.Aggregated{
		{Voters: 3, VerifiedPower: 3, TotalPower: 4},
		{Voters: 3, VerifiedPower: 3, TotalPower: 4},
	}, got, "steps run in order with the same aggregate")
	ev := lastEvent(ctx)
	require.Equal(t, protocol.EventTypeProtocolVotes, ev.Type)
	require.Equal(t, "3", attr(ev, protocol.AttributeKeyVerifiedPower))
}

// Totality (design §5.9): PreBlock never fails; unusable input is skipped with an event.
func TestPreBlockTotality(t *testing.T) {
	ran := false
	pb := protocol.NewPreBlocker(fakeStore{}, func(sdk.Context, protocol.Aggregated) { ran = true })

	cases := map[string][][]byte{
		protocol.SkipMissingTx0: nil,
		protocol.SkipBadTx0:     {[]byte("not tx0")},
	}
	for reason, txs := range cases {
		t.Run(reason, func(t *testing.T) {
			ctx := testCtx()
			require.NotPanics(t, func() { pb.PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 5, Txs: txs}) })
			ev := lastEvent(ctx)
			require.Equal(t, protocol.EventTypeProtocolSkipped, ev.Type)
			require.Equal(t, reason, attr(ev, protocol.AttributeKeyReason))
		})
	}
	require.False(t, ran)

	// Before VEs are active nothing runs and nothing is emitted.
	ctx := testCtx()
	pb.PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 1})
	require.Empty(t, ctx.EventManager().Events())
	require.False(t, ran)

	// A tx0 whose votes are all unknown still runs the steps (with zero verified power).
	ctx = testCtx()
	ext, _ := commitFor(t, testKeys(2), 4, 0, protocol.MinimalVE(4))
	pb.PreBlock(ctx, &abci.RequestFinalizeBlock{Height: 5, Txs: [][]byte{protocol.EncodeInjected(ext)}})
	require.True(t, ran)
}
