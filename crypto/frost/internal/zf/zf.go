// FLAG FOR AUDIT: the cgo boundary to the Zcash Foundation FROST crates.

// Package zf calls the keystone-frost Rust crate (rust/), a C ABI over the Zcash
// Foundation frost-secp256k1-tr / frost-core 3.0.0 crates, through cgo. The static
// library is built by `make zf-lib` into lib/<goos>_<goarch>/ and linked with zig cc
// (see the Makefile). Only crypto/frost and crypto/frost/secret import this package.
//
// Every function copies its inputs into C-readable Go memory and its outputs back; the
// Rust side keeps no pointers and catches its own panics (ErrPanic).
package zf

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/lib/windows_amd64 -lkeystone_frost -lunwind -lws2_32 -luserenv -lntdll -lbcrypt -ladvapi32
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/lib/linux_amd64 -lkeystone_frost -lunwind -lpthread -ldl -lm
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/lib/linux_arm64 -lkeystone_frost -lunwind -lpthread -ldl -lm
#include "keystone_frost.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

var (
	// ErrInvalid is an invalid input or a failed check.
	ErrInvalid = errors.New("frost: invalid input")
	// ErrPanic is a panic caught inside the Rust library (a bug).
	ErrPanic = errors.New("frost: internal error in the FROST library")
)

// CommitmentLen is the encoded length of one signer's commitment (id ‖ D ‖ E).
const CommitmentLen = 68

// BindingInputLen is the length of a binding-factor preimage.
const BindingInputLen = 129

func code(rc C.int32_t) error {
	switch rc {
	case 0:
		return nil
	case 1:
		return ErrInvalid
	default:
		return ErrPanic
	}
}

func in(b []byte) *C.uint8_t {
	if len(b) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&b[0]))
}

func out(b []byte) *C.uint8_t { return (*C.uint8_t)(unsafe.Pointer(&b[0])) }

func size(b []byte) C.size_t { return C.size_t(len(b)) }

// Key returns 02 ‖ x(Q), the effective verifying key for group key gk and tweak t
// (nil or 32 bytes).
func Key(gk [33]byte, tweak []byte) ([33]byte, error) {
	var vk [33]byte
	err := code(C.ksf_key(in(gk[:]), in(tweak), size(tweak), out(vk[:])))
	return vk, err
}

// Session is the public input of one signing operation.
type Session struct {
	GroupKey    [33]byte
	Tweak       []byte // nil or the 32-byte tweak scalar
	Msg         []byte
	Commitments []byte // CommitmentLen bytes per signer, increasing ids
}

// Check fails unless the session's commitments are well formed and its group
// commitment is not the identity.
func (s *Session) Check() error {
	return code(C.ksf_session_check(in(s.GroupKey[:]), in(s.Tweak), size(s.Tweak), in(s.Msg), size(s.Msg), in(s.Commitments), size(s.Commitments)))
}

// BindingFactor returns signer id's binding-factor preimage and binding factor ρ.
func (s *Session) BindingFactor(id uint16) (input [BindingInputLen]byte, rho [32]byte, err error) {
	err = code(C.ksf_binding_factor(in(s.GroupKey[:]), in(s.Tweak), size(s.Tweak), in(s.Msg), size(s.Msg), in(s.Commitments), size(s.Commitments),
		C.uint16_t(id), out(input[:]), out(rho[:])))
	return
}

// VerifyShares checks the combined share z of signers ids against their raw verifying
// shares ys (same order).
func (s *Session) VerifyShares(ids []uint16, ys [][33]byte, z [32]byte) bool {
	if len(ids) == 0 || len(ids) != len(ys) {
		return false
	}
	idb := make([]byte, 0, 2*len(ids))
	yb := make([]byte, 0, 33*len(ys))
	for k, id := range ids {
		idb = append(idb, byte(id>>8), byte(id))
		yb = append(yb, ys[k][:]...)
	}
	return C.ksf_verify_shares(in(s.GroupKey[:]), in(s.Tweak), size(s.Tweak), in(s.Msg), size(s.Msg), in(s.Commitments), size(s.Commitments),
		in(idb), in(yb), C.size_t(len(ids)), in(z[:])) == 0
}

// Aggregate returns the 64-byte BIP-340 signature from shares summing to its z (at
// most one per signer); it fails if the signature does not verify.
func (s *Session) Aggregate(zs [][32]byte) ([64]byte, error) {
	var sig [64]byte
	flat := make([]byte, 0, 32*len(zs))
	for _, z := range zs {
		flat = append(flat, z[:]...)
	}
	err := code(C.ksf_aggregate(in(s.GroupKey[:]), in(s.Tweak), size(s.Tweak), in(s.Msg), size(s.Msg), in(s.Commitments), size(s.Commitments),
		in(flat), C.size_t(len(zs)), out(sig[:])))
	return sig, err
}

// Verify is BIP-340 verification of sig on msg under vk (33 bytes).
func Verify(vk [33]byte, msg, sig []byte) bool {
	return C.ksf_verify(in(vk[:]), in(msg), size(msg), in(sig), size(sig)) == 0
}

// NonceGenerate is RFC 9591 nonce_generate: H3(random ‖ share).
func NonceGenerate(random, share [32]byte) ([32]byte, error) {
	var n [32]byte
	err := code(C.ksf_nonce_generate(in(random[:]), in(share[:]), out(n[:])))
	return n, err
}

// NonceCommit returns (d·G, e·G).
func NonceCommit(hiding, binding [32]byte) (d, e [33]byte, err error) {
	err = code(C.ksf_nonce_commit(in(hiding[:]), in(binding[:]), out(d[:]), out(e[:])))
	return
}

// Sign returns signer id's share for the raw share s of the group key and nonces
// (d, e), which must match id's commitment.
func (s *Session) Sign(id uint16, share, hiding, binding [32]byte) ([32]byte, error) {
	var z [32]byte
	err := code(C.ksf_sign(in(s.GroupKey[:]), in(s.Tweak), size(s.Tweak), in(s.Msg), size(s.Msg), in(s.Commitments), size(s.Commitments),
		C.uint16_t(id), in(share[:]), in(hiding[:]), in(binding[:]), out(z[:])))
	return z, err
}

// EvalPolynomial is f(x) for coefficients a_0..a_{t−1}.
func EvalPolynomial(coeffs [][32]byte, x uint16) ([32]byte, error) {
	var r [32]byte
	flat := make([]byte, 0, 32*len(coeffs))
	for _, c := range coeffs {
		flat = append(flat, c[:]...)
	}
	err := code(C.ksf_eval_polynomial(in(flat), C.size_t(len(coeffs)), C.uint16_t(x), out(r[:])))
	return r, err
}

// EvalCommitment is Σ_j id^j·C_j.
func EvalCommitment(cs [][33]byte, id uint16) ([33]byte, error) {
	var r [33]byte
	flat := make([]byte, 0, 33*len(cs))
	for _, c := range cs {
		flat = append(flat, c[:]...)
	}
	err := code(C.ksf_eval_commitment(in(flat), C.size_t(len(cs)), C.uint16_t(id), out(r[:])))
	return r, err
}

// ProveKnowledge is a dealer's 64-byte proof of knowledge of a0 bound to ctx; rand
// feeds the nonce (at least 32 bytes).
func ProveKnowledge(ctx []byte, id uint16, a0 [32]byte, rand []byte) ([64]byte, error) {
	var p [64]byte
	err := code(C.ksf_pok_prove(in(ctx), size(ctx), C.uint16_t(id), in(a0[:]), in(rand), size(rand), out(p[:])))
	return p, err
}

// VerifyKnowledge checks a dealer's proof of knowledge for C_0 bound to ctx.
func VerifyKnowledge(ctx []byte, id uint16, c0 [33]byte, proof []byte) bool {
	return C.ksf_pok_verify(in(ctx), size(ctx), C.uint16_t(id), in(c0[:]), in(proof), size(proof)) == 0
}

func testPanic() error { return code(C.ksf_test_panic()) }
