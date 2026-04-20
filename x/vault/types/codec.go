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
	legacy.RegisterAminoMsg(cdc, &MsgUpdateParams{}, "keystone/vault/MsgUpdateParams")
	legacy.RegisterAminoMsg(cdc, &MsgOpenVault{}, "keystone/vault/MsgOpenVault")
	legacy.RegisterAminoMsg(cdc, &MsgMint{}, "keystone/vault/MsgMint")
	legacy.RegisterAminoMsg(cdc, &MsgRepay{}, "keystone/vault/MsgRepay")
	legacy.RegisterAminoMsg(cdc, &MsgWithdrawCollateral{}, "keystone/vault/MsgWithdrawCollateral")
	legacy.RegisterAminoMsg(cdc, &MsgCloseVault{}, "keystone/vault/MsgCloseVault")
	legacy.RegisterAminoMsg(cdc, &MsgDepositClaim{}, "keystone/vault/MsgDepositClaim")
	legacy.RegisterAminoMsg(cdc, &MsgDevCreditCollateral{}, "keystone/vault/MsgDevCreditCollateral")
	cdc.RegisterConcrete(&Params{}, "keystone/x/vault/Params", nil)
}

// RegisterInterfaces registers the module's Msg implementations. The DevMsg service
// descriptor is registered only in devcredit builds (keeper/dev_devcredit.go).
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateParams{}, &MsgOpenVault{}, &MsgMint{}, &MsgRepay{},
		&MsgWithdrawCollateral{}, &MsgCloseVault{}, &MsgDepositClaim{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}

// RegisterDevInterfaces registers MsgDevCreditCollateral (devcredit builds only).
func RegisterDevInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil), &MsgDevCreditCollateral{})
	msgservice.RegisterMsgServiceDesc(registry, &_DevMsg_serviceDesc)
}
