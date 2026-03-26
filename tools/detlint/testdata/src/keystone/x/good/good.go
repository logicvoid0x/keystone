package good

import (
	"math/big"
	"sort"
	"time"
)

// Integer arithmetic only.
func half(n int64) int64 { return n / 2 }

// The block time is passed in; time.Unix and Time methods are fine.
func expiry(blockTime time.Time, secs int64) time.Time {
	return time.Unix(blockTime.Unix()+secs, 0)
}

// Sorted keys, then range over a slice.
func sum(m map[string]int) int {
	keys := make([]string, 0, len(m))
	for k := range m { //detlint:allow keys are sorted before use
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := 0
	for _, k := range keys {
		s += m[k]
	}
	return s
}

// Sequential code only.
func run(f func()) { f() }

func big1() *big.Int { return big.NewInt(1) }

var scaled = float64(3) //detlint:allow display-only conversion in a test helper
