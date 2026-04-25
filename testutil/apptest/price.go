package apptest

import (
	"sync"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// PriceFeed is a settable price sidecar for the in-process chain. Every validator
// reports the same price (they share one app instance).
type PriceFeed struct {
	mu      sync.Mutex
	payload vetypes.OraclePayload
	ok      bool
}

// Set reports usd (whole dollars) with 3 sources.
func (p *PriceFeed) Set(usd uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.payload = vetypes.OraclePayload{PriceMicroUsd: usd * 1_000_000, NumSources: 3}
	p.ok = true
}

// SetMicro reports a micro-USD price with 3 sources.
func (p *PriceFeed) SetMicro(micro uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.payload = vetypes.OraclePayload{PriceMicroUsd: micro, NumSources: 3}
	p.ok = true
}

// Down makes the feed report nothing (no oracle payload).
func (p *PriceFeed) Down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ok = false
}

// Snapshot implements protocol.PriceSource.
func (p *PriceFeed) Snapshot() (*vetypes.OraclePayload, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ok {
		return nil, false
	}
	out := p.payload
	return &out, true
}
