// Package secret stores FROST secret shares on disk (design §4.4): JSON envelopes
// encrypted with AES-256-GCM under a key derived from the operator passphrase with
// Argon2id. Not state-machine code: it does file I/O and uses crypto/rand.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (RFC 9106 second recommended option).
const (
	kdfName    = "argon2id"
	kdfTime    = 3
	kdfMemory  = 64 * 1024 // KiB
	kdfThreads = 4
	keyLen     = 32
	saltLen    = 16
)

// Envelope is the encrypted file format.
type Envelope struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Time       uint32 `json:"time"`
	MemoryKiB  uint32 `json:"memory_kib"`
	Threads    uint8  `json:"threads"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// ErrDecrypt is returned for a wrong passphrase or a corrupted file.
var ErrDecrypt = errors.New("cannot decrypt: wrong passphrase or corrupted file")

func aead(passphrase []byte, e *Envelope) (cipher.AEAD, error) {
	key := argon2.IDKey(passphrase, e.Salt, e.Time, e.MemoryKiB, e.Threads, keyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext under passphrase. ad is bound as associated data.
func Seal(passphrase, plaintext, ad []byte) (*Envelope, error) {
	e := &Envelope{Version: 1, KDF: kdfName, Time: kdfTime, MemoryKiB: kdfMemory, Threads: kdfThreads, Salt: make([]byte, saltLen)}
	if _, err := rand.Read(e.Salt); err != nil {
		return nil, err
	}
	g, err := aead(passphrase, e)
	if err != nil {
		return nil, err
	}
	e.Nonce = make([]byte, g.NonceSize())
	if _, err := rand.Read(e.Nonce); err != nil {
		return nil, err
	}
	e.Ciphertext = g.Seal(nil, e.Nonce, plaintext, ad)
	return e, nil
}

// Open decrypts an envelope.
func Open(passphrase []byte, e *Envelope, ad []byte) ([]byte, error) {
	if e.Version != 1 || e.KDF != kdfName {
		return nil, fmt.Errorf("unsupported envelope version %d / kdf %q", e.Version, e.KDF)
	}
	g, err := aead(passphrase, e)
	if err != nil {
		return nil, err
	}
	if len(e.Nonce) != g.NonceSize() {
		return nil, ErrDecrypt
	}
	pt, err := g.Open(nil, e.Nonce, e.Ciphertext, ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// WriteFile seals v (JSON) and writes it to path with mode 0600.
func WriteFile(path string, passphrase []byte, v any, ad []byte) error {
	pt, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e, err := Seal(passphrase, pt, ad)
	if err != nil {
		return err
	}
	bz, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, bz, 0o600)
}

// ReadFile reads and opens an envelope written by WriteFile into v.
func ReadFile(path string, passphrase []byte, v any, ad []byte) error {
	bz, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var e Envelope
	if err := json.Unmarshal(bz, &e); err != nil {
		return err
	}
	pt, err := Open(passphrase, &e, ad)
	if err != nil {
		return err
	}
	return json.Unmarshal(pt, v)
}
