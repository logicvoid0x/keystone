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
				{
					RpcMethod: "DepositAddress", Use: "deposit-address [vault-id]", Short: "Query a vault's Bitcoin deposit address for the active epoch",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "vault_id"}},
				},
				{RpcMethod: "ActiveEpoch", Use: "active-epoch", Short: "Query the ACTIVE key epoch and its treasury address"},
				{RpcMethod: "Treasury", Use: "treasury", Short: "Query custody totals: custodied, credited, cap, fee reserve, deficit, anchor"},
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
