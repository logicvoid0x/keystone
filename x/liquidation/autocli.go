package liquidation

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/OWNER/keystone/x/liquidation/types"
)

// AutoCLIOptions implements autocli.HasAutoCLIConfig.
func (AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "Params", Use: "params", Short: "Query the x/liquidation params"},
				{RpcMethod: "Pool", Use: "pool", Short: "Query the stability pool totals and scalars"},
				{
					RpcMethod: "PoolDeposit", Use: "pool-deposit [address]", Short: "Query a compounded deposit and pending BTC gain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "address"}},
				},
				{
					RpcMethod: "Auction", Use: "auction [id]", Short: "Query an auction and its current price",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{RpcMethod: "Auctions", Use: "auctions", Short: "Query every running auction"},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: types.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams", Use: "update-params [params-json]", Short: "Replace the x/liquidation params; signed by the x/admin admin",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "params"}},
				},
				{
					RpcMethod: "DepositPool", Use: "deposit-pool [amount-ukusd]", Short: "Deposit KUSD into the stability pool",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "amount"}},
				},
				{
					RpcMethod: "WithdrawPool", Use: "withdraw-pool [amount-ukusd]", Short: "Withdraw KUSD from the stability pool (0 claims BTC gains only)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "amount"}},
				},
				{
					RpcMethod: "TakeCollateral", Use: "take-collateral [auction-id] [max-sat] [max-price-micro-usd]", Short: "Buy auctioned BTC with KUSD",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "auction_id"}, {ProtoField: "max_sat"}, {ProtoField: "max_price"}},
				},
			},
		},
	}
}
