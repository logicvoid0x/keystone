package price

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// Defaults (design §6.2).
const (
	DefaultPollInterval = time.Second
	DefaultMaxAge       = 10 * time.Second
	DefaultMinSources   = 3
	requestTimeout      = 800 * time.Millisecond
)

// FetcherConfig configures a Fetcher.
type FetcherConfig struct {
	Sources      []Source
	PollInterval time.Duration
	MaxAge       time.Duration
	MinSources   int
	Now          func() time.Time // defaults to time.Now
}

type snapshot struct {
	payload vetypes.OraclePayload
	at      time.Time
	ok      bool
}

// Fetcher polls its sources in the background and keeps the median of fresh quotes.
// ExtendVote reads Snapshot, which never blocks on network I/O.
type Fetcher struct {
	cfg FetcherConfig

	mu     sync.Mutex
	last   map[string]Quote // latest quote per source name
	snap   snapshot
	cancel context.CancelFunc
	done   chan struct{}
}

// NewFetcher builds a Fetcher; zero config fields take the defaults.
func NewFetcher(cfg FetcherConfig) *Fetcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultMaxAge
	}
	if cfg.MinSources <= 0 {
		cfg.MinSources = DefaultMinSources
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Fetcher{cfg: cfg, last: map[string]Quote{}}
}

// Start polls every PollInterval until Stop.
func (f *Fetcher) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	f.cancel = cancel
	f.done = make(chan struct{})
	go func() {
		defer close(f.done)
		t := time.NewTicker(f.cfg.PollInterval)
		defer t.Stop()
		f.Poll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				f.Poll(ctx)
			}
		}
	}()
}

// Stop ends polling and waits for the goroutine.
func (f *Fetcher) Stop() {
	if f.cancel != nil {
		f.cancel()
		<-f.done
		f.cancel = nil
	}
}

// Poll fetches every source once (concurrently) and recomputes the snapshot.
func (f *Fetcher) Poll(ctx context.Context) {
	type result struct {
		name string
		q    Quote
		err  error
	}
	results := make(chan result, len(f.cfg.Sources))
	var wg sync.WaitGroup
	for _, s := range f.cfg.Sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, requestTimeout)
			defer cancel()
			q, err := s.Fetch(rctx)
			results <- result{s.Name(), q, err}
		}(s)
	}
	wg.Wait()
	close(results)

	f.mu.Lock()
	defer f.mu.Unlock()
	for r := range results {
		if r.err == nil {
			f.last[r.name] = r.q
		}
	}
	f.recompute()
}

// recompute takes the median of quotes younger than MaxAge. Caller holds mu.
func (f *Fetcher) recompute() {
	now := f.cfg.Now()
	var prices []uint64
	for _, s := range f.cfg.Sources {
		q, ok := f.last[s.Name()]
		if ok && now.Sub(q.Time) <= f.cfg.MaxAge {
			prices = append(prices, q.PriceMicroUSD)
		}
	}
	if len(prices) < f.cfg.MinSources {
		f.snap = snapshot{}
		return
	}
	slices.Sort(prices)
	f.snap = snapshot{
		payload: vetypes.OraclePayload{PriceMicroUsd: Median(prices), NumSources: uint32(len(prices))},
		at:      now,
		ok:      true,
	}
}

// Median of sorted prices: the lower median, matching the on-chain rule (§6.3).
func Median(sorted []uint64) uint64 { return sorted[(len(sorted)-1)/2] }

// Snapshot returns the current payload, or false when there is none or it is older
// than MaxAge (a stalled fetcher must not keep reporting an old price).
func (f *Fetcher) Snapshot() (*vetypes.OraclePayload, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.snap.ok || f.cfg.Now().Sub(f.snap.at) > f.cfg.MaxAge {
		return nil, false
	}
	p := f.snap.payload
	return &p, true
}
