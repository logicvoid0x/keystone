package types

import "context"

// RemovalGuard decides whether a MsgSetValidators may apply. before and after hold
// every operator record, sorted by operator address, as they are now and as they
// would be. x/custody provides the real guard in M5 (design §9.4).
type RemovalGuard interface {
	AllowSetValidators(ctx context.Context, before, after []Operator) error
}

// AllowAll is the default guard. It allows every change.
type AllowAll struct{}

func (AllowAll) AllowSetValidators(context.Context, []Operator, []Operator) error { return nil }
