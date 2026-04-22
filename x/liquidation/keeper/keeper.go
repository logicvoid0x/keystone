package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/OWNER/keystone/x/liquidation/types"
	stablecointypes "github.com/OWNER/keystone/x/stablecoin/types"
)

// Keeper owns the stability pool, liquidation selection/execution and the fallback
// Dutch auctions (design §8).
type Keeper struct {
	Schema        collections.Schema
	Params        collections.Item[types.Params]
	Scalars       collections.Item[types.PoolScalars]
	Deposits      collections.Map[sdk.AccAddress, types.PoolDeposit]
	Auctions      collections.Map[uint64, types.Auction]
	NextAuctionID collections.Sequence
	EpochScaleSum collections.Map[collections.Pair[uint64, uint64], sdkmath.Int]
	TotalDeposits collections.Item[sdkmath.Int]
	PendingSat    collections.Item[sdkmath.Int]
	Errors        collections.Item[types.PoolErrors]

	admin      types.AdminKeeper
	oracle     types.OracleKeeper
	vault      types.VaultKeeper
	stablecoin types.StablecoinKeeper
	custody    types.CustodyKeeper
	bank       types.BankKeeper
}

// NewKeeper builds the keeper.
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper,
	oracle types.OracleKeeper, vault types.VaultKeeper, stablecoin types.StablecoinKeeper,
	custody types.CustodyKeeper, bank types.BankKeeper,
) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:        collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Scalars:       collections.NewItem(sb, types.PoolScalarsKey, "pool_scalars", codec.CollValue[types.PoolScalars](cdc)),
		Deposits:      collections.NewMap(sb, types.PoolDepositsKey, "pool_deposits", sdk.AccAddressKey, codec.CollValue[types.PoolDeposit](cdc)),
		Auctions:      collections.NewMap(sb, types.AuctionsKey, "auctions", collections.Uint64Key, codec.CollValue[types.Auction](cdc)),
		NextAuctionID: collections.NewSequence(sb, types.NextAuctionIDKey, "next_auction_id"),
		EpochScaleSum: collections.NewMap(sb, types.EpochScaleSumKey, "epoch_scale_sum",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key), sdk.IntValue),
		TotalDeposits: collections.NewItem(sb, types.TotalDepositsKey, "total_deposits", sdk.IntValue),
		PendingSat:    collections.NewItem(sb, types.PendingSatKey, "pending_sat", sdk.IntValue),
		Errors:        collections.NewItem(sb, types.PoolErrorsKey, "pool_errors", codec.CollValue[types.PoolErrors](cdc)),
		admin:         admin,
		oracle:        oracle,
		vault:         vault,
		stablecoin:    stablecoin,
		custody:       custody,
		bank:          bank,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

func kusd(amt sdkmath.Int) sdk.Coins { return sdk.NewCoins(sdk.NewCoin(stablecointypes.Denom, amt)) }

// ModuleBalance is the pool module account's KUSD balance.
func (k *Keeper) ModuleBalance(ctx context.Context) sdkmath.Int {
	return k.bank.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), stablecointypes.Denom).Amount
}

// GetParams returns the current params.
func (k *Keeper) GetParams(ctx context.Context) (types.Params, error) { return k.Params.Get(ctx) }

// PoolBalance is the total of compounded deposits available to absorb debt.
func (k *Keeper) PoolBalance(ctx context.Context) (sdkmath.Int, error) {
	return k.TotalDeposits.Get(ctx)
}

func emit(ctx context.Context, typ string, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(typ, attrs...))
}

// UpdateParams applies MsgUpdateParams: the signer must be the x/poa admin.
func (k *Keeper) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.admin.CheckAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	return k.Params.Set(ctx, msg.Params)
}

// InitGenesis stores params and an empty pool (P = 1e18, epoch 0, scale 0).
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	z := sdkmath.ZeroInt()
	if err := k.Scalars.Set(ctx, types.PoolScalars{P: types.DecimalPrecision}); err != nil {
		return err
	}
	if err := k.TotalDeposits.Set(ctx, z); err != nil {
		return err
	}
	if err := k.PendingSat.Set(ctx, z); err != nil {
		return err
	}
	return k.Errors.Set(ctx, types.PoolErrors{CollError: z, DebtError: z})
}

// ExportGenesis returns the params.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p}, nil
}
