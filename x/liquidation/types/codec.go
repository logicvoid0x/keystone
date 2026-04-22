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
	legacy.RegisterAminoMsg(cdc, &MsgUpdateParams{}, "keystone/liquidation/MsgUpdateParams")
	legacy.RegisterAminoMsg(cdc, &MsgDepositPool{}, "keystone/liquidation/MsgDepositPool")
	legacy.RegisterAminoMsg(cdc, &MsgWithdrawPool{}, "keystone/liquidation/MsgWithdrawPool")
	legacy.RegisterAminoMsg(cdc, &MsgTakeCollateral{}, "keystone/liquidation/MsgTakeCollateral")
	cdc.RegisterConcrete(&Params{}, "keystone/x/liquidation/Params", nil)
}

// RegisterInterfaces registers the module's Msg implementations.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateParams{}, &MsgDepositPool{}, &MsgWithdrawPool{}, &MsgTakeCollateral{})
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
