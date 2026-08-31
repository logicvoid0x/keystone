package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers the amino names used for multisig signing.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgUpdateParams{}, "keystone/custody/MsgUpdateParams")
	legacy.RegisterAminoMsg(cdc, &MsgWithdrawClaim{}, "keystone/custody/MsgWithdrawClaim")
	legacy.RegisterAminoMsg(cdc, &MsgRegisterCustodyKey{}, "keystone/custody/MsgRegisterCustodyKey")
	legacy.RegisterAminoMsg(cdc, &MsgDKGCommit{}, "keystone/custody/MsgDKGCommit")
	legacy.RegisterAminoMsg(cdc, &MsgDKGDeal{}, "keystone/custody/MsgDKGDeal")
	legacy.RegisterAminoMsg(cdc, &MsgDKGComplaint{}, "keystone/custody/MsgDKGComplaint")
	cdc.RegisterConcrete(&Params{}, "keystone/x/custody/Params", nil)
}

// RegisterInterfaces registers the module's Msg implementations.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil), &MsgUpdateParams{}, &MsgWithdrawClaim{},
		&MsgRegisterCustodyKey{}, &MsgDKGCommit{}, &MsgDKGDeal{}, &MsgDKGComplaint{})
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
