package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"

	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/OWNER/keystone/x/vault/types"
)

// Keeper holds the x/vault params.
type Keeper struct {
	Schema collections.Schema
	Params collections.Item[types.Params]

	admin types.AdminKeeper
}

// NewKeeper builds the keeper. admin authorizes MsgUpdateParams (x/poa).
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params: collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		admin:  admin,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// GetParams returns the current params.
func (k *Keeper) GetParams(ctx context.Context) (types.Params, error) { return k.Params.Get(ctx) }

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

// InitGenesis stores the params.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	return k.Params.Set(ctx, gs.Params)
}

// ExportGenesis returns the params.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p}, nil
}
