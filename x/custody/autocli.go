package custody

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/OWNER/keystone/x/custody/types"
)

// AutoCLIOptions implements autocli.HasAutoCLIConfig.
func (AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "Params", Use: "params", Short: "Query the x/custody params"},
				{
					RpcMethod: "Claimable", Use: "claimable [address]", Short: "Query an account's claimable BTC (satoshis)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "address"}},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: types.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams", Use: "update-params [params-json]", Short: "Replace the x/custody params; signed by the x/poa admin",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "params"}},
				},
			},
		},
	}
}
