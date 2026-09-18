package app

import (
	"context"

	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/OWNER/keystone/app/protocol"
)

// UpgradeV2 is the upgrade that moves vote extensions to version 2 (the M6 upgrade
// drill, design §17). It changes no store layout.
const UpgradeV2 = "v2"

// VESchedule is the VE version for votes at height: version 2 once UpgradeV2 was
// applied at a height U and height > U, else version 1. The votes of height U itself
// are cast by binaries that stop at U, so they are still version 1 (docs/decisions.md).
func (app *App) VESchedule(ctx context.Context, height int64) uint32 {
	done, err := app.UpgradeKeeper.GetDoneHeight(ctx, UpgradeV2)
	if err == nil && done > 0 && height > done {
		return protocol.VEVersion + 1
	}
	return protocol.VEVersion
}

// runMigrations is the upgrade handler body: the module manager's migrations (none
// change state in v2).
func (app *App) runMigrations(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
	return app.ModuleManager.RunMigrations(ctx, app.configurator, fromVM)
}

// FlagDeferUpgradeHandlers (tests only) leaves the upgrade handlers out of app.New, so
// the app behaves like the release before an upgrade until RegisterUpgradeHandlers.
const FlagDeferUpgradeHandlers = "keystone.test.defer-upgrade-handlers"

// RegisterUpgradeHandlers installs the handlers this binary knows (tests that deferred
// them call it at the upgrade height, as a restarted node with the new binary would).
func (app *App) RegisterUpgradeHandlers() { app.registerUpgrades() }
