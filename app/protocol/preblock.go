package protocol

import (
	"context"
	"strconv"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	sdkmath "cosmossdk.io/math"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	custodytypes "github.com/OWNER/keystone/x/custody/types"
	liqtypes "github.com/OWNER/keystone/x/liquidation/types"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
)

// Events emitted by the protocol segment (docs/decisions.md; §11.3 names none for the
// orchestrator).
const (
	EventTypeProtocolSkipped    = "protocol_skipped"
	EventTypeProtocolVotes      = "protocol_votes"
	EventTypeProtocolStepFailed = "protocol_step_failed"

	AttributeKeyReason        = "reason"
	AttributeKeyVoters        = "voters"
	AttributeKeyVerifiedPower = "verified_power"
	AttributeKeyTotalPower    = "total_power"
	AttributeKeyStep          = "step"
	AttributeKeyError         = "error"

	SkipMissingTx0 = "missing_tx0"
	SkipBadTx0     = "undecodable_tx0"

	StepAggregate    = "aggregate"
	StepOracle       = "oracle"
	StepDrip         = "drip"
	StepLiqSelect    = "liquidation_select"
	StepLiqExecute   = "liquidation_execute"
	StepCustodyBuild = "custody_build"
	StepCustodyOps   = "custody_apply"
)

// OracleKeeper is x/oracle as used by the PreBlocker.
type OracleKeeper interface {
	OracleView
	ApplyAggregate(ctx context.Context, agg oracletypes.PriceAggregate) error
	LiquidationPrice(ctx context.Context) (sdkmath.Int, bool, error)
}

// VaultKeeper is x/vault as used by the PreBlocker.
type VaultKeeper interface {
	Drip(ctx context.Context, blockTime time.Time) error
}

// LiquidationKeeper is x/liquidation as used by the PreBlocker (design §4.2).
type LiquidationKeeper interface {
	Select(ctx context.Context, price sdkmath.Int) (liqtypes.LiquidationBatch, error)
	Execute(ctx context.Context, batch liqtypes.LiquidationBatch, price sdkmath.Int) error
}

// CustodyKeeper is x/custody as used by the PreBlocker (design §9.7).
type CustodyKeeper interface {
	BuildOps(ctx context.Context, votes []custodytypes.Vote) (custodytypes.CustodyBatch, error)
	ApplyOps(ctx context.Context, batch custodytypes.CustodyBatch) error
}

// Keepers are the modules the protocol segment calls, in order.
type Keepers struct {
	Oracle      OracleKeeper
	Vault       VaultKeeper
	Liquidation LiquidationKeeper
	Custody     CustodyKeeper
}

// PreBlocker runs the protocol segment on the real (non-branched) state.
type PreBlocker struct {
	valStore     ValidatorStore
	keepers      Keepers
	onUnverified func(consAddr []byte)
}

// SetUnverifiedObserver installs a callback told about every committed vote whose VE
// did not verify (metric ve_unverified{validator}). It only observes.
func (p *PreBlocker) SetUnverifiedObserver(f func(consAddr []byte)) { p.onUnverified = f }

// NewPreBlocker builds the orchestrator.
func NewPreBlocker(vs ValidatorStore, k Keepers) *PreBlocker {
	return &PreBlocker{valStore: vs, keepers: k}
}

// PreBlock is design §5.9: decode tx0, verify the votes, aggregate, then
// oracle.ApplyAggregate → vault.Drip → liquidation.Select → liquidation.Execute →
// custody.BuildOps → custody.ApplyOps.
//
// Totality: PreBlock never returns an error. Each step runs in a cached context; a
// step that fails has its writes discarded, emits protocol_step_failed and the next
// step still runs. A block without a usable tx0 can only reach here if ProcessProposal
// was bypassed; the segment is skipped with an event.
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
	if p.onUnverified != nil {
		reportUnverified(ext, votes, p.onUnverified)
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeProtocolVotes,
		sdk.NewAttribute(AttributeKeyVoters, strconv.Itoa(len(votes))),
		sdk.NewAttribute(AttributeKeyVerifiedPower, strconv.FormatInt(verified, 10)),
		sdk.NewAttribute(AttributeKeyTotalPower, strconv.FormatInt(total, 10)),
	))

	k := p.keepers
	var agg Aggregated
	if !runStep(ctx, StepAggregate, func(c sdk.Context) (err error) {
		agg, err = Aggregate(c, k.Oracle, votes, verified, total)
		return err
	}) {
		agg = Aggregated{Voters: len(votes), VerifiedPower: verified, TotalPower: total}
	}

	runStep(ctx, StepOracle, func(c sdk.Context) error { return k.Oracle.ApplyAggregate(c, agg.Oracle) })
	runStep(ctx, StepDrip, func(c sdk.Context) error { return k.Vault.Drip(c, c.BlockTime()) })

	var (
		batch   liqtypes.LiquidationBatch
		price   sdkmath.Int
		haveLiq bool
	)
	runStep(ctx, StepLiqSelect, func(c sdk.Context) error {
		pr, ok, err := k.Oracle.LiquidationPrice(c)
		if err != nil || !ok {
			return err // STALE: no liquidations this block
		}
		b, err := k.Liquidation.Select(c, pr)
		if err != nil {
			return err
		}
		batch, price, haveLiq = b, pr, true
		return nil
	})
	if haveLiq {
		runStep(ctx, StepLiqExecute, func(c sdk.Context) error { return k.Liquidation.Execute(c, batch, price) })
	}
	var cb custodytypes.CustodyBatch
	if runStep(ctx, StepCustodyBuild, func(c sdk.Context) (err error) {
		cb, err = k.Custody.BuildOps(c, agg.Custody)
		return err
	}) && !cb.Empty() {
		runStep(ctx, StepCustodyOps, func(c sdk.Context) error { return k.Custody.ApplyOps(c, cb) })
	}
}

// runStep runs f in a cached context and commits its writes and events only if it
// succeeds. It reports whether f succeeded.
func runStep(ctx sdk.Context, name string, f func(sdk.Context) error) bool {
	cc, write := ctx.CacheContext()
	if err := f(cc); err != nil {
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeProtocolStepFailed,
			sdk.NewAttribute(AttributeKeyStep, name),
			sdk.NewAttribute(AttributeKeyError, err.Error())))
		ctx.Logger().Error("protocol step failed; skipped", "step", name, "err", err)
		return false
	}
	write()
	return true
}

func emitSkip(ctx sdk.Context, reason string) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeProtocolSkipped, sdk.NewAttribute(AttributeKeyReason, reason)))
}

// reportUnverified calls f for each commit vote that VerifyVotes did not verify, in
// commit order.
func reportUnverified(ext abci.ExtendedCommitInfo, votes []VerifiedVote, f func([]byte)) {
	i := 0
	for _, v := range ext.Votes {
		if v.BlockIdFlag != cmtproto.BlockIDFlagCommit {
			continue
		}
		if i < len(votes) && string(votes[i].ConsAddr) == string(v.Validator.Address) {
			i++ // verified votes keep commit order
			continue
		}
		f(v.Validator.Address)
	}
}
