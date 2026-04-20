//go:build devcredit

package vault

import (
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/OWNER/keystone/x/vault/keeper"
	"github.com/OWNER/keystone/x/vault/types"
)

// DevCreditEnabled reports whether this binary routes MsgDevCreditCollateral.
const DevCreditEnabled = true

func registerDevInterfaces(r cdctypes.InterfaceRegistry) { types.RegisterDevInterfaces(r) }

func registerDevServices(cfg module.Configurator, k *keeper.Keeper) {
	types.RegisterDevMsgServer(cfg.MsgServer(), keeper.NewDevMsgServerImpl(k))
}
