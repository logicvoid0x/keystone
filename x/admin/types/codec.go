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
	legacy.RegisterAminoMsg(cdc, &MsgUpdateParams{}, "keystone/admin/MsgUpdateParams")
	legacy.RegisterAminoMsg(cdc, &MsgSetAllowlist{}, "keystone/admin/MsgSetAllowlist")
	legacy.RegisterAminoMsg(cdc, &MsgExec{}, "keystone/admin/MsgExec")
	cdc.RegisterConcrete(&Params{}, "keystone/x/admin/Params", nil)
}

// RegisterInterfaces registers the module's Msg implementations.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateParams{},
		&MsgSetAllowlist{},
		&MsgExec{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
