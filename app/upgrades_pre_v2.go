//go:build keystone_pre_v2

package app

// registerUpgrades: the pre-v2 release knows no upgrade (it halts at the v2 height).
func (app *App) registerUpgrades() {}
