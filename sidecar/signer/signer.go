// FLAG FOR AUDIT: signer side of the FROST session protocol (design §9.9, §9.11).

// Package signer is the FROST signer sidecar (spec M4a-T6): at ExtendVote it reads the
// committed custody state, signs every OPEN session assigned to this node's shares,
// tops up its nonce pools and returns the VE FrostPayload. It is not state-machine code:
// it holds secret shares, does file I/O and uses crypto/rand. Its output only reaches
// state through vote extensions, where every share is verified.
package signer

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"

	"cosmossdk.io/log/v2"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
	"github.com/OWNER/keystone/sidecar/signer/noncestore"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// ReplenishPerBlock is how many new commitments a share offers per VE (§9.9 rule 6).
const ReplenishPerBlock = 2

// crashPoint, when set (crash tests only), is called at each step of signing a
// session: "bind" before the nonces are bound, "bound" after, "signed" after the
// share is computed.
var crashPoint func(stage string)

func stage(s string) {
	if crashPoint != nil {
		crashPoint(s)
	}
}

// EpochView is the committed state of one epoch this node holds a share of.
type EpochView struct {
	Number   uint64
	GroupKey []byte
	Index    uint32
	// LastRecorded is the highest seq the chain recorded for the share; Pool its
	// unconsumed commitments.
	LastRecorded uint64
	Pool         []vetypes.NonceCommitment
	// Sessions are the OPEN sessions with an assignment for Index, in id order.
	Sessions []custodytypes.SigningSession
}

// View is what the signer reads from committed state at each ExtendVote.
type View struct {
	NoncePool int
	// Epochs are the ACTIVE or RETIRING epochs among those asked for, ascending.
	Epochs []EpochView
}

// ChainView loads a View for the given shares (epoch → share index).
type ChainView interface {
	Load(mine map[uint64]uint32) (View, error)
}

// Shares gives the node's secret shares.
type Shares interface {
	// Epochs lists the epochs a share is held for.
	Epochs() ([]uint64, error)
	// Share returns the share index and secret for an epoch.
	Share(epoch uint64) (uint32, frost.Scalar, error)
}

// Signer produces FrostPayloads. Safe for concurrent use.
type Signer struct {
	view   ChainView
	shares Shares
	store  *noncestore.Store
	logger log.Logger
	alarm  func(error)

	mu          sync.Mutex
	forceRevoke bool
	cache       map[string]vetypes.CombinedShare
}

// Config builds a Signer.
type Config struct {
	View   ChainView
	Shares Shares
	Store  *noncestore.Store
	Logger log.Logger
	// Alarm is told about refusals that indicate a nonce-safety problem (a nonce
	// bound to another session). Defaults to logging.
	Alarm func(error)
	// RevokeNonces sets revoke_all_nonces in the next payload (--revoke-nonces, §9.5);
	// it is also set when the nonce database was missing or corrupt.
	RevokeNonces bool
}

// New returns a signer.
func New(cfg Config) *Signer {
	s := &Signer{view: cfg.View, shares: cfg.Shares, store: cfg.Store, logger: cfg.Logger, alarm: cfg.Alarm,
		forceRevoke: cfg.RevokeNonces || cfg.Store.Fresh, cache: map[string]vetypes.CombinedShare{}}
	if s.logger == nil {
		s.logger = log.NewNopLogger()
	}
	if s.alarm == nil {
		s.alarm = func(err error) { s.logger.Error("FROST SIGNER ALARM", "err", err) }
	}
	return s
}

// Payload is the §9.11 signer side for the VE at height: for every OPEN session
// assigned to one of our shares, the combined share (cached per session; nonces
// bound before computing); fresh commitments up to the pool size, at most
// ReplenishPerBlock per share (persisted before they are returned); and
// revoke_all_nonces when forced or when the chain holds an unconsumed commitment of
// ours that the nonce store does not have. Lists are in canonical order.
func (s *Signer) Payload(height int64) (*vetypes.FrostPayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	epochs, err := s.shares.Epochs()
	if err != nil {
		return nil, err
	}
	mine := make(map[uint64]uint32, len(epochs))
	secrets := make(map[uint64]frost.Scalar, len(epochs))
	for _, e := range epochs {
		idx, sh, err := s.shares.Share(e)
		if err != nil {
			return nil, fmt.Errorf("share of epoch %d: %w", e, err)
		}
		mine[e], secrets[e] = idx, sh
	}
	v, err := s.view.Load(mine)
	if err != nil {
		return nil, err
	}
	out := &vetypes.FrostPayload{}
	revoke := s.forceRevoke
	live := map[string]bool{}
	for _, ev := range v.Epochs {
		share := secrets[ev.Number]
		for _, c := range ev.Pool {
			stored, ok, err := s.store.Lookup(ev.Number, ev.Index, c.Seq)
			if err != nil {
				return nil, err
			}
			if !ok || !bytes.Equal(stored.Hiding[:], c.Hiding) || !bytes.Equal(stored.Binding[:], c.Binding) {
				revoke = true
			}
		}
		for _, sess := range ev.Sessions {
			live[string(sess.Id)] = true
			if cs, ok := s.cache[string(sess.Id)]; ok {
				out.Shares = append(out.Shares, cs)
				continue
			}
			cs, err := s.sign(ev, share, sess)
			if err != nil {
				if errors.Is(err, noncestore.ErrBoundElsewhere) {
					s.alarm(err)
				} else {
					s.logger.Error("cannot sign session", "session", fmt.Sprintf("%x", sess.Id), "err", err)
				}
				continue
			}
			s.cache[string(sess.Id)] = cs
			out.Shares = append(out.Shares, cs)
		}
		want := min(ReplenishPerBlock, v.NoncePool-len(ev.Pool))
		for j := 1; j <= want; j++ {
			c, err := s.store.Commit(ev.Number, ev.Index, ev.LastRecorded+uint64(j), share, nil)
			if err != nil {
				return nil, err
			}
			out.Commitments = append(out.Commitments, vetypes.NonceCommitment{
				Epoch: ev.Number, ShareIndex: ev.Index, Seq: c.Seq, Hiding: bytes.Clone(c.Hiding[:]), Binding: bytes.Clone(c.Binding[:]),
			})
		}
	}
	for id := range s.cache {
		if !live[id] {
			delete(s.cache, id)
		}
	}
	if revoke {
		out.RevokeAllNonces = true
		s.forceRevoke = false
	}
	slices.SortFunc(out.Commitments, func(a, b vetypes.NonceCommitment) int {
		return cmp.Or(cmp.Compare(a.Epoch, b.Epoch), cmp.Compare(a.ShareIndex, b.ShareIndex), cmp.Compare(a.Seq, b.Seq))
	})
	slices.SortFunc(out.Shares, func(a, b vetypes.CombinedShare) int { return bytes.Compare(a.SessionId, b.SessionId) })
	return out, nil
}

// sign binds the assigned nonces to the session (fsync) and computes the combined
// share z = Σ z_i over this node's assignments.
func (s *Signer) sign(ev EpochView, share frost.Scalar, sess custodytypes.SigningSession) (vetypes.CombinedShare, error) {
	key, err := frost.NewKey(frost.Taproot, ev.GroupKey, sess.Tweak)
	if err != nil {
		return vetypes.CombinedShare{}, err
	}
	cs := make([]frost.Commitment, len(sess.Signers))
	for i, a := range sess.Signers {
		if cs[i], err = frost.ParseCommitment(a.ShareIndex, a.Hiding, a.Binding); err != nil {
			return vetypes.CombinedShare{}, err
		}
	}
	sg, err := frost.NewSigning(key, sess.Msg, cs)
	if err != nil {
		return vetypes.CombinedShare{}, err
	}
	var z frost.Scalar
	n := 0
	for _, a := range sess.Signers {
		if a.ShareIndex != ev.Index {
			continue
		}
		stage("bind")
		nonces, err := s.store.Bind(ev.Number, a.ShareIndex, a.NonceSeq, sess.Id)
		if err != nil {
			return vetypes.CombinedShare{}, err
		}
		stage("bound")
		zi, err := secret.Sign(sg, a.ShareIndex, share, nonces)
		if err != nil {
			return vetypes.CombinedShare{}, err
		}
		stage("signed")
		z.Add(&zi)
		n++
	}
	if n == 0 {
		return vetypes.CombinedShare{}, errors.New("no assignment for our share")
	}
	zb := z.Bytes()
	return vetypes.CombinedShare{SessionId: bytes.Clone(sess.Id), Z: zb[:]}, nil
}
