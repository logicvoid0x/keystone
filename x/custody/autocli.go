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
				{
					RpcMethod: "Withdrawal", Use: "withdrawal [id]", Short: "Query a withdrawal request",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod: "Batch", Use: "batch [id]", Short: "Query a withdrawal batch (current txid and signed tx hex)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{RpcMethod: "Batches", Use: "batches", Short: "Query every withdrawal batch"},
				{
					RpcMethod: "Session", Use: "session [id-hex]", Short: "Query a FROST signing session",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{RpcMethod: "ActiveSessions", Use: "active-sessions", Short: "Query the OPEN and STALLED signing sessions"},
				{
					RpcMethod: "DKG", Use: "dkg [epoch]", Short: "Query a DKG run and its messages",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "epoch"}},
				},
				{RpcMethod: "CustodyKeys", Use: "custody-keys", Short: "Query the registered custody encryption and hot keys"},
				{
					RpcMethod: "NoncePool", Use: "nonce-pool [epoch] [share-index]", Short: "Query a share's unconsumed nonce commitments",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "epoch"}, {ProtoField: "share_index"}},
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
				{
					RpcMethod: "RegisterCustodyKey", Use: "register-custody-key [enc-pubkey-hex] [hot-key]", Short: "Set your DKG encryption key and custody hot key (operators added after genesis)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "enc_pubkey"}, {ProtoField: "hot_key"}},
				},
				{RpcMethod: "DKGCommit", Skip: true},
				{RpcMethod: "DKGDeal", Skip: true},
				{RpcMethod: "DKGComplaint", Skip: true},
				{
					RpcMethod: "WithdrawClaim", Use: "withdraw-claim [amount-sat] [recipient-script-hex]", Short: "Withdraw claimable BTC to a Bitcoin script (P2TR, P2WPKH or P2WSH)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "amount_sat"}, {ProtoField: "recipient_script"}},
				},
			},
		},
	}
}
