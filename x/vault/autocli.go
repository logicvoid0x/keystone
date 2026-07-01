package vault

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/OWNER/keystone/x/vault/types"
)

// AutoCLIOptions implements autocli.HasAutoCLIConfig.
func (AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	tx := &autocliv1.ServiceCommandDescriptor{
		Service: types.Msg_serviceDesc.ServiceName,
		RpcCommandOptions: []*autocliv1.RpcCommandOptions{
			{
				RpcMethod: "UpdateParams", Use: "update-params [params-json]", Short: "Replace the x/vault params; signed by the x/poa admin",
				PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "params"}},
			},
			{RpcMethod: "OpenVault", Use: "open-vault", Short: "Open a vault"},
			{
				RpcMethod: "Mint", Use: "mint [vault-id] [amount-ukusd]", Short: "Mint KUSD against a vault",
				PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "vault_id"}, {ProtoField: "amount"}},
			},
			{
				RpcMethod: "Repay", Use: "repay [vault-id] [amount-ukusd]", Short: "Repay a vault's debt (anyone may repay)",
				PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "vault_id"}, {ProtoField: "amount"}},
			},
			{
				RpcMethod: "WithdrawCollateral", Use: "withdraw-collateral [vault-id] [amount-sat]", Short: "Withdraw collateral (M1: to your claimable balance)",
				PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "vault_id"}, {ProtoField: "amount_sat"}},
			},
			{
				RpcMethod: "CloseVault", Use: "close-vault [vault-id]", Short: "Close a vault with no debt and withdraw all collateral",
				PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "vault_id"}},
			},
			{
				RpcMethod: "DepositClaim", Use: "deposit-claim [vault-id] [amount-sat]", Short: "Move claimable BTC into a vault",
				PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "vault_id"}, {ProtoField: "amount_sat"}},
			},
		},
	}
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "Params", Use: "params", Short: "Query the x/vault params"},
				{
					RpcMethod: "Vault", Use: "vault [id]", Short: "Query a vault and its debt",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod: "VaultsByOwner", Use: "vaults-by-owner [owner]", Short: "Query an owner's vaults",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "owner"}},
				},
				{RpcMethod: "RateState", Use: "rate-state", Short: "Query the stability-fee rate index and total art"},
				{RpcMethod: "LiquidationQueue", Use: "liquidation-queue", Short: "Query the worst vaults first (--limit)"},
			},
		},
		Tx: tx,
	}
}
