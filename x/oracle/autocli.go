package oracle

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/OWNER/keystone/x/oracle/types"
)

// AutoCLIOptions implements autocli.HasAutoCLIConfig.
func (AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "Params", Use: "params", Short: "Query the x/oracle params"},
				{RpcMethod: "Price", Use: "price", Short: "Query the committed price state (spot, TWAP, status)"},
				{RpcMethod: "Twap", Use: "twap", Short: "Query the TWAP and the number of blocks it covers"},
				{
					RpcMethod: "MissCounter", Use: "miss-counter [operator-address]", Short: "Query an operator's oracle misses",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator"}},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: types.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams", Use: "update-params [params-json]", Short: "Replace the x/oracle params; signed by the x/admin admin",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "params"}},
				},
			},
		},
	}
}
