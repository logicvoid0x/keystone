// FLAG FOR AUDIT: nonce safety (design §9.9). A nonce bound to two sessions leaks the
// share.

// Package noncestore is the signer's FROST nonce database (design §9.9, spec M4a-T2):
// a bbolt file at <home>/custody/nonces.db, mode 0600, fsynced on every write. It
// keeps, per (epoch, share index, seq), the secret nonces (d, e), their public
// commitments (D, E) and the session the nonces were bound to. Never restore this file
// from a backup (§9.5).
package noncestore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
)

// FileName is the database file under <home>/custody.
const FileName = "nonces.db"

// Path is <home>/custody/nonces.db.
func Path(home string) string { return filepath.Join(home, "custody", FileName) }

var bucketNonces = []byte("nonces")

// ErrBoundElsewhere means the nonce is already bound to a different session. The
// caller must not sign and should raise an alarm.
var ErrBoundElsewhere = errors.New("nonce already bound to another session")

// ErrUnknown means the store has no nonce for the requested (epoch, share, seq).
var ErrUnknown = errors.New("no such nonce")

// Commitment is a stored nonce's public part.
type Commitment struct {
	Epoch   uint64
	Share   uint32
	Seq     uint64
	Hiding  [33]byte
	Binding [33]byte
}

// Store is an open nonce database. Methods are safe for one process; bbolt holds an
// exclusive file lock.
type Store struct {
	db *bolt.DB
	// Fresh reports that the database was missing or corrupt at Open and was
	// recreated: the signer must set revoke_all_nonces (§9.9 rule 5).
	Fresh bool
}

// Open opens (or creates) the database at path. A missing file, or one that fails to
// open or whose records fail the integrity check, is replaced by an empty database
// (the corrupt one is renamed aside) and Fresh is set.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	fresh := errors.Is(statErr, os.ErrNotExist)
	db, err := open(path)
	if err == nil && !fresh {
		if cerr := check(db); cerr != nil {
			_ = db.Close()
			err = cerr
		}
	}
	if err != nil {
		if fresh {
			return nil, err
		}
		aside := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
		if rerr := os.Rename(path, aside); rerr != nil {
			return nil, fmt.Errorf("nonce db %s is corrupt (%v) and cannot be moved aside: %w", path, err, rerr)
		}
		fresh = true
		if db, err = open(path); err != nil {
			return nil, err
		}
	}
	return &Store{db: db, Fresh: fresh}, nil
}

func open(path string) (*bolt.DB, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketNonces)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// check decodes every record and recomputes its commitments.
func check(db *bolt.DB) error {
	return db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketNonces).ForEach(func(k, v []byte) error {
			if len(k) != keyLen {
				return fmt.Errorf("bad key length %d", len(k))
			}
			r, err := decode(v)
			if err != nil {
				return err
			}
			d, e, err := r.nonces.Commitment()
			if err != nil || d != r.hiding || e != r.binding {
				return errors.New("nonce record does not match its commitments")
			}
			return nil
		})
	})
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const keyLen = 8 + 4 + 8

func key(epoch uint64, share uint32, seq uint64) []byte {
	k := make([]byte, keyLen)
	binary.BigEndian.PutUint64(k[0:], epoch)
	binary.BigEndian.PutUint32(k[8:], share)
	binary.BigEndian.PutUint64(k[12:], seq)
	return k
}

type record struct {
	nonces  secret.Nonces
	hiding  [33]byte
	binding [33]byte
	usedFor []byte // session id, empty when unbound
}

const recVersion = 1

func (r record) encode() []byte {
	d, e := r.nonces.Hiding.Bytes(), r.nonces.Binding.Bytes()
	out := make([]byte, 0, 1+32+32+33+33+1+len(r.usedFor))
	out = append(out, recVersion)
	out = append(out, d[:]...)
	out = append(out, e[:]...)
	out = append(out, r.hiding[:]...)
	out = append(out, r.binding[:]...)
	out = append(out, byte(len(r.usedFor)))
	return append(out, r.usedFor...)
}

func decode(v []byte) (record, error) {
	var r record
	const fixed = 1 + 32 + 32 + 33 + 33 + 1
	if len(v) < fixed || v[0] != recVersion || len(v) != fixed+int(v[fixed-1]) {
		return r, errors.New("malformed nonce record")
	}
	var err error
	if r.nonces.Hiding, err = frost.ParseScalar(v[1:33]); err != nil {
		return r, err
	}
	if r.nonces.Binding, err = frost.ParseScalar(v[33:65]); err != nil {
		return r, err
	}
	copy(r.hiding[:], v[65:98])
	copy(r.binding[:], v[98:131])
	r.usedFor = bytes.Clone(v[fixed:])
	return r, nil
}

// Commit returns the commitment for (epoch, share, seq), generating and persisting
// fresh nonces first if the store has none (§9.9 rule 1: persisted before it is
// published; the same seq always returns the same commitment). rng is crypto/rand
// when nil.
func (s *Store) Commit(epoch uint64, share uint32, seq uint64, secretShare frost.Scalar, rng io.Reader) (Commitment, error) {
	c := Commitment{Epoch: epoch, Share: share, Seq: seq}
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketNonces)
		k := key(epoch, share, seq)
		if v := b.Get(k); v != nil {
			r, err := decode(v)
			if err != nil {
				return err
			}
			c.Hiding, c.Binding = r.hiding, r.binding
			return nil
		}
		n, err := secret.NewNonces(frost.Taproot, rng, secretShare)
		if err != nil {
			return err
		}
		d, e, err := n.Commitment()
		if err != nil {
			return err
		}
		c.Hiding, c.Binding = d, e
		return b.Put(k, record{nonces: n, hiding: d, binding: e}.encode())
	})
	return c, err
}

// Lookup returns the stored commitment for (epoch, share, seq), if any.
func (s *Store) Lookup(epoch uint64, share uint32, seq uint64) (Commitment, bool, error) {
	c := Commitment{Epoch: epoch, Share: share, Seq: seq}
	found := false
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketNonces).Get(key(epoch, share, seq))
		if v == nil {
			return nil
		}
		r, err := decode(v)
		if err != nil {
			return err
		}
		c.Hiding, c.Binding, found = r.hiding, r.binding, true
		return nil
	})
	return c, found, err
}

// Bind binds (epoch, share, seq) to sessionID and returns its secret nonces (§9.9 rule
// 3). The binding is committed (fsync) before Bind returns. Binding again to the same
// session returns the same nonces; binding to a different one fails with
// ErrBoundElsewhere.
func (s *Store) Bind(epoch uint64, share uint32, seq uint64, sessionID []byte) (secret.Nonces, error) {
	if len(sessionID) == 0 || len(sessionID) > 255 {
		return secret.Nonces{}, errors.New("bad session id")
	}
	var out secret.Nonces
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketNonces)
		k := key(epoch, share, seq)
		v := b.Get(k)
		if v == nil {
			return ErrUnknown
		}
		r, err := decode(v)
		if err != nil {
			return err
		}
		switch {
		case len(r.usedFor) == 0:
			r.usedFor = bytes.Clone(sessionID)
			if err := b.Put(k, r.encode()); err != nil {
				return err
			}
		case !bytes.Equal(r.usedFor, sessionID):
			return fmt.Errorf("%w: epoch %d share %d seq %d is bound to %x, not %x", ErrBoundElsewhere, epoch, share, seq, r.usedFor, sessionID)
		}
		out = r.nonces
		return nil
	})
	return out, err
}

// BoundTo returns the session (epoch, share, seq) is bound to (nil if unbound).
func (s *Store) BoundTo(epoch uint64, share uint32, seq uint64) ([]byte, error) {
	var used []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketNonces).Get(key(epoch, share, seq))
		if v == nil {
			return ErrUnknown
		}
		r, err := decode(v)
		used = r.usedFor
		return err
	})
	return used, err
}
