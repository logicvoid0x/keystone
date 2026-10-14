// Package bounds holds the range checks used by every module's Params.Validate.
package bounds

import (
	"fmt"

	sdkmath "cosmossdk.io/math"
)

// MaxBps is 100% in basis points.
const MaxBps = 10_000

// Uint checks lo <= v <= hi.
func Uint(name string, v, lo, hi uint64) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s = %d, want %d..%d", name, v, lo, hi)
	}
	return nil
}

// Int checks lo <= v <= hi. A nil v (unset field) is rejected.
func Int(name string, v, lo, hi sdkmath.Int) error {
	if v.IsNil() {
		return fmt.Errorf("%s is unset", name)
	}
	if v.LT(lo) || v.GT(hi) {
		return fmt.Errorf("%s = %s, want %s..%s", name, v, lo, hi)
	}
	return nil
}

// Dec checks lo <= v <= hi. A nil v (unset field) is rejected.
func Dec(name string, v, lo, hi sdkmath.LegacyDec) error {
	if v.IsNil() {
		return fmt.Errorf("%s is unset", name)
	}
	if v.LT(lo) || v.GT(hi) {
		return fmt.Errorf("%s = %s, want %s..%s", name, v, lo, hi)
	}
	return nil
}

// First returns the first non-nil error.
func First(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
