//go:build !devcredit

package vault

import (
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/OWNER/keystone/x/vault/keeper"
)

// DevCreditEnabled reports whether this binary routes MsgDevCreditCollateral.
const DevCreditEnabled = false

func registerDevInterfaces(cdctypes.InterfaceRegistry) {}

func registerDevServices(module.Configurator, *keeper.Keeper) {}
