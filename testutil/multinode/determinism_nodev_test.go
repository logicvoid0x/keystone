//go:build !devcredit

package multinode_test

import "testing"

// TestDeterminism10k needs the devcredit build tag: until M3 adds Bitcoin deposits, the
// dev collateral message is the only way the workload can fund vaults. Fail loudly
// rather than pass without running.
func TestDeterminism10k(t *testing.T) {
	t.Fatal("run with -tags devcredit (e.g. make determinism-long, or go test -tags long,devcredit ./testutil/multinode -run TestDeterminism10k)")
}
