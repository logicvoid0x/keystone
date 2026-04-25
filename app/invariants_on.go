//go:build invariants

package app

// InvariantsEnabled: this build checks every invariant at the end of every block and
// panics on a violation (design Appendix A, spec M1-T8).
const InvariantsEnabled = true
