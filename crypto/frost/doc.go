// FLAG FOR AUDIT: the whole package.

// Package frost is FROST threshold Schnorr signing over secp256k1 (RFC 9591) in the
// Taproot variant of the Zcash Foundation frost-secp256k1-tr crate (design §9.6,
// §9.11), matched byte for byte against its test vectors (testdata/).
//
// This package holds only public, deterministic operations and is imported by the
// state machine (x/custody): binding factors, group commitment, Lagrange coefficients,
// challenge, per-signer share verification, aggregation, BIP-340 verification and the
// DKG public checks. Secret operations (nonce generation, signature shares, trusted
// dealer keygen) live in crypto/frost/secret, which only sidecars import.
package frost
