package stablecoin

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/logicvoid0x/keystone/x/stablecoin/types"
)

// AutoCLIOptions implements autocli.HasAutoCLIConfig.
func (AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "Params", Use: "params", Short: "Query the x/stablecoin params"},
				{RpcMethod: "Surplus", Use: "surplus", Short: "Query the surplus buffer (ukusd)"},
				{RpcMethod: "BadDebt", Use: "bad-debt", Short: "Query bad debt (sin) and the mint pause"},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: types.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams", Use: "update-params [params-json]", Short: "Replace the x/stablecoin params; signed by the x/admin admin",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "params"}},
				},
			},
		},
	}
}
