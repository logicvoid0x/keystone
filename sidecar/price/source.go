// Package price is the price-fetcher sidecar (design §4.3, §6.2). It is not
// state-machine code: it does network I/O and runs a goroutine. Its only output to
// the chain is the OraclePayload that ExtendVote copies into the vote extension.
package price

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Quote is one exchange's BTC/USD price.
type Quote struct {
	PriceMicroUSD uint64
	Time          time.Time
}

// Source fetches one exchange's BTC/USD price.
type Source interface {
	Name() string
	Fetch(ctx context.Context) (Quote, error)
}

// ParseUSD converts a decimal USD string ("100000.00") to micro-USD without floats.
// Digits beyond 6 decimal places are truncated.
func ParseUSD(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty price")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	frac += strings.Repeat("0", 6-len(frac))
	var v uint64
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid price %q", s)
		}
		next := v*10 + uint64(c-'0')
		if next/10 != v {
			return 0, fmt.Errorf("price %q overflows", s)
		}
		v = next
	}
	if v == 0 {
		return 0, fmt.Errorf("price %q is zero", s)
	}
	return v, nil
}

// FormatUSD renders micro-USD as a USD string with two decimals (truncated).
func FormatUSD(micro uint64) string {
	cents := micro / 10_000
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
