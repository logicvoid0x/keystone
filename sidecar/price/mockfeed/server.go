// Package mockfeed is a controllable BTC/USD feed with three fake exchanges (a, b, c),
// used by tests and localnet (spec M1-T1).
//
//	GET  /ex/{a|b|c}/btcusd   -> {"price":"100000.00","ts":<unix>}
//	POST /admin/price  {"usd":"76000"}              base price
//	POST /admin/offset {"exchange":"a","bps":30}     skew one exchange
//	POST /admin/down   {"exchange":"a"}              503 from one exchange ("*" = all)
//	POST /admin/up     {"exchange":"a"}              undo down ("*" = all)
//	GET  /admin/state                                current settings
package mockfeed

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/OWNER/keystone/sidecar/price"
)

// Exchanges are the fake exchange names.
var Exchanges = []string{"a", "b", "c"}

// Server is the mock feed.
type Server struct {
	mu        sync.Mutex
	baseMicro uint64
	offsetBps map[string]int64
	down      map[string]bool
	now       func() time.Time
}

// New returns a feed at baseUSD (e.g. "100000").
func New(baseUSD string, now func() time.Time) (*Server, error) {
	p, err := price.ParseUSD(baseUSD)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Server{baseMicro: p, offsetBps: map[string]int64{}, down: map[string]bool{}, now: now}, nil
}

func known(ex string) bool {
	for _, e := range Exchanges {
		if e == ex {
			return true
		}
	}
	return false
}

// PriceMicro returns exchange ex's current price in micro-USD.
func (s *Server) PriceMicro(ex string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseMicro * uint64(10_000+s.offsetBps[ex]) / 10_000
}

// Handler serves the feed and admin endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ex/{ex}/btcusd", func(w http.ResponseWriter, r *http.Request) {
		ex := r.PathValue("ex")
		if !known(ex) {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		isDown := s.down[ex]
		s.mu.Unlock()
		if isDown {
			http.Error(w, "exchange down", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]any{"price": price.FormatUSD(s.PriceMicro(ex)), "ts": s.now().Unix()})
	})
	mux.HandleFunc("POST /admin/price", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			USD string `json:"usd"`
		}
		if !decode(w, r, &req) {
			return
		}
		p, err := price.ParseUSD(req.USD)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.baseMicro = p
		s.mu.Unlock()
		writeJSON(w, s.state())
	})
	mux.HandleFunc("POST /admin/offset", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Exchange string `json:"exchange"`
			Bps      int64  `json:"bps"`
		}
		if !decode(w, r, &req) {
			return
		}
		if !known(req.Exchange) || req.Bps <= -10_000 {
			http.Error(w, "bad exchange or bps", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.offsetBps[req.Exchange] = req.Bps
		s.mu.Unlock()
		writeJSON(w, s.state())
	})
	setDown := func(v bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Exchange string `json:"exchange"`
			}
			if !decode(w, r, &req) {
				return
			}
			targets := []string{req.Exchange}
			if req.Exchange == "*" {
				targets = Exchanges
			} else if !known(req.Exchange) {
				http.Error(w, "bad exchange", http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			for _, ex := range targets {
				s.down[ex] = v
			}
			s.mu.Unlock()
			writeJSON(w, s.state())
		}
	}
	mux.HandleFunc("POST /admin/down", setDown(true))
	mux.HandleFunc("POST /admin/up", setDown(false))
	mux.HandleFunc("GET /admin/state", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.state()) })
	return mux
}

func (s *Server) state() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex := map[string]any{}
	for _, e := range Exchanges {
		ex[e] = map[string]any{"offset_bps": s.offsetBps[e], "down": s.down[e]}
	}
	return map[string]any{"usd": price.FormatUSD(s.baseMicro), "exchanges": ex}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
