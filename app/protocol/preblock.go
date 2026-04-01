package protocol

import (
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Events emitted by the protocol segment. Names outside design §11.3: recorded in
// docs/decisions.md.
const (
	EventTypeProtocolSkipped = "protocol_skipped"
	EventTypeProtocolVotes   = "protocol_votes"

	AttributeKeyReason        = "reason"
	AttributeKeyVoters        = "voters"
	AttributeKeyVerifiedPower = "verified_power"
	AttributeKeyTotalPower    = "total_power"

	SkipMissingTx0 = "missing_tx0"
	SkipBadTx0     = "undecodable_tx0"
)

// Step is one module step of the protocol segment, run in order after aggregation
// (design §5.9: oracle, drip, liquidations, custody). A step must not fail for a tx0
// that ProcessProposal accepted; it skips what cannot apply and emits an event.
type Step func(ctx sdk.Context, agg Aggregated)

// PreBlocker runs the protocol segment on the real (non-branched) state.
type PreBlocker struct {
	valStore ValidatorStore
	steps    []Step
}

// NewPreBlocker builds the orchestrator. M0 has no module steps.
func NewPreBlocker(vs ValidatorStore, steps ...Step) *PreBlocker {
	return &PreBlocker{valStore: vs, steps: steps}
}

// PreBlock decodes tx0, verifies the votes, aggregates and runs the module steps
// (design §5.9). Totality: it never returns an error. A block without a usable tx0
// can only reach here if ProcessProposal is bypassed (tests, replay of a bug); it is
// skipped with an event.
func (p *PreBlocker) PreBlock(ctx sdk.Context, req *abci.RequestFinalizeBlock) {
	if !VEActive(ctx, req.Height) {
		return
	}
	// The protocol segment's work is bounded by per-block caps, not gas (design §4.5).
	ctx = ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())

	if len(req.Txs) == 0 {
		emitSkip(ctx, SkipMissingTx0)
		return
	}
	ext, ok := DecodeInjected(req.Txs[0])
	if !ok {
		emitSkip(ctx, SkipBadTx0)
		return
	}
	votes, verified, total := VerifyVotes(ctx, p.valStore, ctx.ChainID(), req.Height-1, ext)
	agg := Aggregate(votes, verified, total)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeProtocolVotes,
		sdk.NewAttribute(AttributeKeyVoters, strconv.Itoa(agg.Voters)),
		sdk.NewAttribute(AttributeKeyVerifiedPower, strconv.FormatInt(verified, 10)),
		sdk.NewAttribute(AttributeKeyTotalPower, strconv.FormatInt(total, 10)),
	))
	for _, step := range p.steps {
		step(ctx, agg)
	}
}

func emitSkip(ctx sdk.Context, reason string) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeProtocolSkipped, sdk.NewAttribute(AttributeKeyReason, reason)))
}
