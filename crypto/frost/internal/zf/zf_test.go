package zf

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPanicIsCaught: a panic in the Rust library comes back as ErrPanic and never
// unwinds into Go.
func TestPanicIsCaught(t *testing.T) {
	require.ErrorIs(t, testPanic(), ErrPanic)
}

// TestInvalidInputs: malformed inputs are errors, not crashes.
func TestInvalidInputs(t *testing.T) {
	_, err := Key([33]byte{}, nil)
	require.ErrorIs(t, err, ErrInvalid)
	s := Session{Msg: []byte("m"), Commitments: make([]byte, CommitmentLen)}
	require.ErrorIs(t, s.Check(), ErrInvalid)
	require.False(t, s.VerifyShares([]uint16{1}, [][33]byte{{}}, [32]byte{}))
	_, err = EvalCommitment(nil, 1)
	require.ErrorIs(t, err, ErrInvalid)
	require.False(t, VerifyKnowledge(nil, 1, [33]byte{}, nil))
	_, err = ProveKnowledge(nil, 1, [32]byte{1}, make([]byte, 8))
	require.ErrorIs(t, err, ErrPanic, "randomness exhausted")
}
