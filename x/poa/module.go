package poa

import (
	"context"
	"encoding/json"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"

	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/OWNER/keystone/x/poa/keeper"
	"github.com/OWNER/keystone/x/poa/types"
)

const consensusVersion = 1

var (
	_ module.AppModuleBasic  = AppModule{}
	_ module.HasServices     = AppModule{}
	_ module.HasABCIGenesis  = AppModule{}
	_ module.HasABCIEndBlock = AppModule{}
	_ appmodule.AppModule    = AppModule{}
)

// AppModule is the x/poa module.
type AppModule struct {
	cdc    codec.Codec
	keeper *keeper.Keeper
}

// NewAppModule builds the module.
func NewAppModule(cdc codec.Codec, k *keeper.Keeper) AppModule {
	return AppModule{cdc: cdc, keeper: k}
}

func (AppModule) Name() string                                    { return types.ModuleName }
func (AppModule) IsOnePerModuleType()                             {}
func (AppModule) IsAppModule()                                    {}
func (AppModule) ConsensusVersion() uint64                        { return consensusVersion }
func (AppModule) RegisterLegacyAminoCodec(c *codec.LegacyAmino)   { types.RegisterLegacyAminoCodec(c) }
func (AppModule) RegisterInterfaces(r cdctypes.InterfaceRegistry) { types.RegisterInterfaces(r) }

func (AppModule) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *gwruntime.ServeMux) {
	if err := types.RegisterQueryHandlerClient(context.Background(), mux, types.NewQueryClient(clientCtx)); err != nil {
		panic(err)
	}
}

func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQuerier(am.keeper))
}

func (AppModule) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesis())
}

func (AppModule) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := cdc.UnmarshalJSON(bz, &gs); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return gs.Validate()
}

// InitGenesis stores the operators and returns the initial validator set.
func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, bz json.RawMessage) []abci.ValidatorUpdate {
	var gs types.GenesisState
	cdc.MustUnmarshalJSON(bz, &gs)
	updates, err := am.keeper.InitGenesis(ctx, &gs)
	if err != nil {
		panic(fmt.Errorf("%s InitGenesis: %w", types.ModuleName, err))
	}
	return updates
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	gs, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	return cdc.MustMarshalJSON(gs)
}

// EndBlock returns validator updates for operators whose power changed.
func (am AppModule) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	return am.keeper.EndBlocker(ctx)
}

// AutoCLIOptions implements autocli.HasAutoCLIConfig.
func (AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "Params", Use: "params", Short: "Query the x/poa params"},
				{RpcMethod: "Validators", Use: "validators", Short: "List every operator, sorted by operator address"},
				{
					RpcMethod:      "Validator",
					Use:            "validator [address]",
					Short:          "Query one operator",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "address"}},
				},
				{RpcMethod: "TotalPower", Use: "total-power", Short: "Query the total operator power"},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: types.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "SetValidators",
					Use:       "set-validators [entries-json]",
					Short:     "Add (power 1) or remove (power 0) operators; signed by the admin",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "entries", Varargs: true},
					},
				},
				{
					RpcMethod:      "UpdateParams",
					Use:            "update-params [params-json]",
					Short:          "Replace the x/poa params (including the admin); signed by the admin",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "params"}},
				},
			},
		},
	}
}
