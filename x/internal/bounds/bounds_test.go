package bounds_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/internal/bounds"
)

func TestUint(t *testing.T) {
	require.NoError(t, bounds.Uint("x", 5, 1, 10))
	require.NoError(t, bounds.Uint("x", 1, 1, 10))
	require.NoError(t, bounds.Uint("x", 10, 1, 10))
	require.Error(t, bounds.Uint("x", 0, 1, 10))
	require.Error(t, bounds.Uint("x", 11, 1, 10))
}

func TestInt(t *testing.T) {
	lo, hi := sdkmath.NewInt(1), sdkmath.NewInt(10)
	require.NoError(t, bounds.Int("x", sdkmath.NewInt(5), lo, hi))
	require.Error(t, bounds.Int("x", sdkmath.NewInt(0), lo, hi))
	require.Error(t, bounds.Int("x", sdkmath.NewInt(11), lo, hi))
	require.Error(t, bounds.Int("x", sdkmath.Int{}, lo, hi))
}

func TestDec(t *testing.T) {
	lo, hi := sdkmath.LegacyZeroDec(), sdkmath.LegacyOneDec()
	require.NoError(t, bounds.Dec("x", sdkmath.LegacyNewDecWithPrec(5, 1), lo, hi))
	require.NoError(t, bounds.Dec("x", hi, lo, hi))
	require.Error(t, bounds.Dec("x", sdkmath.LegacyNewDec(2), lo, hi))
	require.Error(t, bounds.Dec("x", sdkmath.LegacyNewDec(-1), lo, hi))
	require.Error(t, bounds.Dec("x", sdkmath.LegacyDec{}, lo, hi))
}

func TestFirst(t *testing.T) {
	a, b := errors.New("a"), errors.New("b")
	require.NoError(t, bounds.First(nil, nil))
	require.Equal(t, a, bounds.First(nil, a, b))
}
