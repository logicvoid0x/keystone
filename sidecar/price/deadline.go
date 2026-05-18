package price

import (
	"time"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// ExtendVoteDeadline is how long ExtendVote waits for a snapshot (design §4.3).
const ExtendVoteDeadline = 200 * time.Millisecond

// Snapshotter is a price sidecar snapshot (protocol.PriceSource).
type Snapshotter interface {
	Snapshot() (*vetypes.OraclePayload, bool)
}

// Deadline wraps a Snapshotter so a slow or stuck source yields no payload after the
// deadline instead of blocking ExtendVote. The goroutine lives here, in the sidecar,
// because state-machine code (app/protocol) may not start goroutines.
type Deadline struct {
	inner Snapshotter
	d     time.Duration
}

// WithDeadline returns src bounded by d.
func WithDeadline(src Snapshotter, d time.Duration) *Deadline { return &Deadline{inner: src, d: d} }

type snap struct {
	p  *vetypes.OraclePayload
	ok bool
}

// Snapshot returns the inner snapshot, or nothing if it takes longer than the deadline.
func (s *Deadline) Snapshot() (*vetypes.OraclePayload, bool) {
	ch := make(chan snap, 1) // buffered: a late result does not leak a blocked goroutine
	go func() {
		p, ok := s.inner.Snapshot()
		ch <- snap{p, ok}
	}()
	t := time.NewTimer(s.d)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.p, r.ok
	case <-t.C:
		return nil, false
	}
}

// Stop stops the inner source if it can be stopped.
func (s *Deadline) Stop() {
	if st, ok := s.inner.(interface{ Stop() }); ok {
		st.Stop()
	}
}
