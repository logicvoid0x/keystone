//go:build !invariants

package app

// InvariantsEnabled: release builds do not check invariants in EndBlock.
const InvariantsEnabled = false
