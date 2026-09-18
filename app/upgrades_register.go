//go:build !keystone_pre_v2

package app

// registerUpgrades installs the upgrade handlers this binary knows. A binary built with
// the keystone_pre_v2 tag (the drill's "old" release) has none, so it stops at the v2
// upgrade height as x/upgrade requires.
func (app *App) registerUpgrades() {
	app.UpgradeKeeper.SetUpgradeHandler(UpgradeV2, app.runMigrations)
}
