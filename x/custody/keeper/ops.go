package keeper

import "context"

// ApplyOps applies the custody batch. No-op until the Bitcoin tallies arrive (M3-T4).
func (k *Keeper) ApplyOps(context.Context) error { return nil }
