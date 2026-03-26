package bad

import (
	"maps"
	"math/rand" // want `import of "math/rand" is not allowed`
	"time"
)

var ratio float64 // want `floating-point type float64`

func half(n int) float32 { // want `floating-point type float32`
	return float32(n) / 2 // want `floating-point type float32`
}

func now() time.Time {
	return time.Now() // want `time.Now is not allowed`
}

func since(t time.Time) time.Duration {
	return time.Since(t) // want `time.Since is not allowed`
}

func until(t time.Time) time.Duration {
	return time.Until(t) // want `time.Until is not allowed`
}

var clock = time.Now // want `time.Now is not allowed`

func sum(m map[string]int) int {
	s := 0
	for _, v := range m { // want `range over a map`
		s += v
	}
	for k := range maps.Keys(m) { // want `range over maps.Keys`
		s += len(k)
	}
	return s
}

type ledger map[string]int

func sumNamed(l ledger) (s int) {
	for _, v := range l { // want `range over a map`
		s += v
	}
	return s
}

func spawn() {
	go func() {}() // want `go statement is not allowed`
}

func roll() int { return rand.Intn(6) }
