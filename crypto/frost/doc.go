// FLAG FOR AUDIT: the whole package.

// Package frost is FROST threshold Schnorr signing over secp256k1 (RFC 9591) in the
// Taproot variant (design §9.6, §9.11). The FROST operations are those of the Zcash
// Foundation frost-secp256k1-tr / frost-core crates, linked through cgo
// (crypto/frost/internal/zf, pinned to 3.0.0) and checked against the crates' test
// vectors (testdata/).
//
// This package holds only public, deterministic operations and is imported by the
// state machine (x/custody): session preparation (binding factors, group commitment,
// challenge), per-signer share verification, aggregation, BIP-340 verification and the
// DKG public checks, plus the group helpers and DLEQ / share-encryption code of
// Keystone's DKG (dleq.go). Secret operations (nonce generation, signature shares,
// dealer keygen, proofs) live in crypto/frost/secret, which only sidecars import.
package frost
