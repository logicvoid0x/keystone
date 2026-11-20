package keeper

import (
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/oracle/types"
)

// Keeper owns the committed price, the TWAP ring and the miss counters (design §6).
type Keeper struct {
	Schema     collections.Schema
	Params     collections.Item[types.Params]
	PriceState collections.Item[types.PriceState]
	TwapRing   collections.Map[uint32, sdkmath.Int]
	TwapMeta   collections.Item[types.TwapMeta]
	MissBits   collections.KeySet[collections.Pair[[]byte, uint32]]
	MissCount  collections.Map[[]byte, uint32]

	admin types.AdminKeeper
	vals  types.ValidatorSet
}

// NewKeeper builds the keeper. admin authorizes MsgUpdateParams; vals is x/poa.
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper, vals types.ValidatorSet) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:     collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		PriceState: collections.NewItem(sb, types.PriceStateKey, "price_state", codec.CollValue[types.PriceState](cdc)),
		TwapRing:   collections.NewMap(sb, types.TwapRingKey, "twap_ring", collections.Uint32Key, sdk.IntValue),
		TwapMeta:   collections.NewItem(sb, types.TwapMetaKey, "twap_meta", codec.CollValue[types.TwapMeta](cdc)),
		MissBits: collections.NewKeySet(sb, types.MissBitsKey, "miss_bits",
			collections.PairKeyCodec(collections.BytesKey, collections.Uint32Key)),
		MissCount: collections.NewMap(sb, types.MissCountKey, "miss_count", collections.BytesKey, collections.Uint32Value),
		admin:     admin,
		vals:      vals,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// InitialPriceState is the genesis price state: no price yet, so STALE.
func InitialPriceState() types.PriceState {
	z := sdkmath.ZeroInt()
	return types.PriceState{SpotMicroUsd: z, LastMedianMicroUsd: z, TwapMicroUsd: z, PrevSpotMicroUsd: z, Status: types.PRICE_STATUS_STALE}
}

func emptyMeta() types.TwapMeta { return types.TwapMeta{Sum: sdkmath.ZeroInt()} }

// GetParams returns the current params.
func (k *Keeper) GetParams(ctx context.Context) (types.Params, error) { return k.Params.Get(ctx) }

// GetPriceState returns the committed price state.
func (k *Keeper) GetPriceState(ctx context.Context) (types.PriceState, error) {
	return k.PriceState.Get(ctx)
}

// LiquidationPrice is the clamped spot; false when STALE or no price (design §6.5).
func (k *Keeper) LiquidationPrice(ctx context.Context) (sdkmath.Int, bool, error) {
	ps, err := k.PriceState.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, false, err
	}
	if ps.Status == types.PRICE_STATUS_STALE || !ps.SpotMicroUsd.IsPositive() {
		return ps.SpotMicroUsd, false, nil
	}
	return ps.SpotMicroUsd, true, nil
}

// CollateralPrice is min(spot, TWAP); false when STALE or no price (design §6.5).
func (k *Keeper) CollateralPrice(ctx context.Context) (sdkmath.Int, bool, error) {
	ps, err := k.PriceState.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, false, err
	}
	p := sdkmath.MinInt(ps.SpotMicroUsd, ps.TwapMicroUsd)
	if ps.Status == types.PRICE_STATUS_STALE || !p.IsPositive() {
		return p, false, nil
	}
	return p, true, nil
}

// IsActiveForLiquidation reports whether liquidations may run (status not STALE).
func (k *Keeper) IsActiveForLiquidation(ctx context.Context) (bool, error) {
	_, ok, err := k.LiquidationPrice(ctx)
	return ok, err
}

// ApplyAggregate commits an oracle aggregate (design §6.3–§6.6). It runs every block
// from the first tx0, with or without an update: the spot (if updated), the TWAP ring
// (a block without an update repeats the last spot), the status and the misses.
func (k *Keeper) ApplyAggregate(ctx context.Context, agg types.PriceAggregate) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	ps, err := k.PriceState.Get(ctx)
	if err != nil {
		return err
	}
	height := sdkCtx.BlockHeight()

	if agg.Updated {
		ps.PrevSpotMicroUsd = ps.SpotMicroUsd
		ps.SpotMicroUsd = agg.Spot
		ps.LastMedianMicroUsd = sdkmath.NewIntFromUint64(agg.Median)
		ps.LastUpdateHeight = height
		var partBps int64
		if agg.TotalPower > 0 {
			partBps = agg.ReportedPower * 10_000 / agg.TotalPower
		}
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypePriceUpdated,
			sdk.NewAttribute(types.AttributeKeySpot, ps.SpotMicroUsd.String()),
			sdk.NewAttribute(types.AttributeKeyMedian, ps.LastMedianMicroUsd.String()),
			sdk.NewAttribute(types.AttributeKeyParticipationBps, strconv.FormatInt(partBps, 10)),
		))
	}

	if ps.SpotMicroUsd.IsPositive() {
		twap, err := k.pushTwap(ctx, ps.SpotMicroUsd, p.TwapBlocks)
		if err != nil {
			return err
		}
		ps.TwapMicroUsd = twap
	}

	status := computeStatus(ps, height, p)
	if status != ps.Status {
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypePriceStatus,
			sdk.NewAttribute(types.AttributeKeyStatus, status.String())))
	}
	ps.Status = status
	if err := k.PriceState.Set(ctx, ps); err != nil {
		return err
	}
	return k.recordMisses(ctx, agg, height, p)
}

// computeStatus: STALE when there is no price or no update for StaleBlocks; else
// VOLATILE when |spot − TWAP| × 10,000 > TWAP × VolatileBps; else ACTIVE.
func computeStatus(ps types.PriceState, height int64, p types.Params) types.PriceStatus {
	if !ps.SpotMicroUsd.IsPositive() || height-ps.LastUpdateHeight >= int64(p.StaleBlocks) {
		return types.PRICE_STATUS_STALE
	}
	if ps.TwapMicroUsd.IsPositive() {
		diff := ps.SpotMicroUsd.Sub(ps.TwapMicroUsd).Abs()
		if diff.MulRaw(10_000).GT(ps.TwapMicroUsd.MulRaw(int64(p.VolatileBps))) {
			return types.PRICE_STATUS_VOLATILE
		}
	}
	return types.PRICE_STATUS_ACTIVE
}

// pushTwap writes spot into the ring and returns the mean of the filled entries
// (floor). The ring holds TwapBlocks entries; until it is full the mean covers the
// entries written so far.
func (k *Keeper) pushTwap(ctx context.Context, spot sdkmath.Int, size uint32) (sdkmath.Int, error) {
	meta, err := k.TwapMeta.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if meta.Count == size {
		old, err := k.TwapRing.Get(ctx, meta.Next)
		if err != nil {
			return sdkmath.Int{}, err
		}
		meta.Sum = meta.Sum.Sub(old)
	} else {
		meta.Count++
	}
	if err := k.TwapRing.Set(ctx, meta.Next, spot); err != nil {
		return sdkmath.Int{}, err
	}
	meta.Sum = meta.Sum.Add(spot)
	meta.Next = (meta.Next + 1) % size
	if err := k.TwapMeta.Set(ctx, meta); err != nil {
		return sdkmath.Int{}, err
	}
	return meta.Sum.QuoRaw(int64(meta.Count)), nil
}

// resetTwap clears the ring (TwapBlocks changed).
func (k *Keeper) resetTwap(ctx context.Context) error {
	if err := k.TwapRing.Clear(ctx, nil); err != nil {
		return err
	}
	return k.TwapMeta.Set(ctx, emptyMeta())
}

// recordMisses updates every active operator's miss window for this block (design
// §6.6). A miss is a VE included in the commit with no usable oracle payload, or a
// price more than OutlierBps from the median. Operators not in the commit do not miss.
func (k *Keeper) recordMisses(ctx context.Context, agg types.PriceAggregate, height int64, p types.Params) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	byAddr := make(map[string]types.Report, len(agg.Reports))
	for _, r := range agg.Reports {
		byAddr[string(r.ConsAddr)] = r
	}
	ops, err := k.vals.ActiveValidators(ctx)
	if err != nil {
		return err
	}
	idx := uint32(height % int64(p.MissWindow))
	for _, op := range ops { // sorted by operator address
		cons := []byte(op.ConsAddress())
		r, included := byAddr[string(cons)]
		missed := false
		if included {
			switch {
			case !types.Usable(r.Payload, p):
				missed = true
			case agg.Updated && types.IsOutlier(r.Payload.PriceMicroUsd, agg.Median, p):
				missed = true
			}
		}
		key := collections.Join(cons, idx)
		was, err := k.MissBits.Has(ctx, key)
		if err != nil {
			return err
		}
		if was == missed {
			continue
		}
		count, err := k.MissCount.Get(ctx, cons)
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if missed {
			count++
			if err := k.MissBits.Set(ctx, key); err != nil {
				return err
			}
		} else {
			count--
			if err := k.MissBits.Remove(ctx, key); err != nil {
				return err
			}
		}
		if count > p.MissAlert {
			sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeOracleMissThreshold,
				sdk.NewAttribute(types.AttributeKeyValidator, op.OperatorAddress),
				sdk.NewAttribute(types.AttributeKeyMisses, strconv.FormatUint(uint64(count), 10)),
			))
			if err := k.MissBits.Clear(ctx, collections.NewPrefixedPairRange[[]byte, uint32](cons)); err != nil {
				return err
			}
			count = 0
		}
		if err := k.MissCount.Set(ctx, cons, count); err != nil {
			return err
		}
	}
	return nil
}

// Misses returns an operator's misses in the current window.
func (k *Keeper) Misses(ctx context.Context, cons []byte) (uint32, error) {
	c, err := k.MissCount.Get(ctx, cons)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	return c, err
}

// resetMisses clears every miss window (MissWindow changed).
func (k *Keeper) resetMisses(ctx context.Context) error {
	if err := k.MissBits.Clear(ctx, nil); err != nil {
		return err
	}
	return k.MissCount.Clear(ctx, nil)
}

// UpdateParams applies MsgUpdateParams (signer = x/poa admin). Changing TwapBlocks
// clears the TWAP ring; changing MissWindow clears the miss windows.
func (k *Keeper) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.admin.CheckAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	old, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if old.TwapBlocks != msg.Params.TwapBlocks {
		if err := k.resetTwap(ctx); err != nil {
			return err
		}
	}
	if old.MissWindow != msg.Params.MissWindow {
		if err := k.resetMisses(ctx); err != nil {
			return err
		}
	}
	return k.Params.Set(ctx, msg.Params)
}

// InitGenesis stores the params and the initial (STALE) price state.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	if err := k.PriceState.Set(ctx, InitialPriceState()); err != nil {
		return err
	}
	return k.TwapMeta.Set(ctx, emptyMeta())
}

// ExportGenesis returns the params. Price state restarts from STALE on import (the
// first update after import is unclamped).
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p}, nil
}
