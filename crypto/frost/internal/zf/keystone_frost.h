/* C ABI of the keystone-frost Rust crate (rust/src/lib.rs). Return codes: 0 ok or
 * valid, 1 invalid input or failed check, 2 caught panic.
 *
 * A session is: gk (33-byte group key P), tw (tw_len = 0 or 32: the Taproot tweak
 * scalar t), msg (msg_len bytes) and cs (cs_len = 68·n bytes: per signer, in strictly
 * increasing id order, id as 2 bytes big-endian ‖ hiding commitment (33) ‖ binding
 * commitment (33)). Scalars are 32 bytes big-endian, points 33-byte compressed. */
#ifndef KEYSTONE_FROST_H
#define KEYSTONE_FROST_H

#include <stddef.h>
#include <stdint.h>

#define KSF_SESSION const uint8_t *gk, const uint8_t *tw, size_t tw_len, \
	const uint8_t *msg, size_t msg_len, const uint8_t *cs, size_t cs_len

int32_t ksf_key(const uint8_t *gk, const uint8_t *tw, size_t tw_len, uint8_t *out);
int32_t ksf_session_check(KSF_SESSION);
int32_t ksf_binding_factor(KSF_SESSION, uint16_t id, uint8_t *out_input, uint8_t *out_rho);
int32_t ksf_verify_shares(KSF_SESSION, const uint8_t *ids, const uint8_t *ys, size_t n_ids, const uint8_t *z);
int32_t ksf_aggregate(KSF_SESSION, const uint8_t *zs, size_t n_zs, uint8_t *out);
int32_t ksf_verify(const uint8_t *vk, const uint8_t *msg, size_t msg_len, const uint8_t *sig, size_t sig_len);

int32_t ksf_nonce_generate(const uint8_t *random, const uint8_t *share, uint8_t *out);
int32_t ksf_nonce_commit(const uint8_t *hiding, const uint8_t *binding, uint8_t *out_d, uint8_t *out_e);
int32_t ksf_sign(KSF_SESSION, uint16_t id, const uint8_t *share, const uint8_t *hiding,
	const uint8_t *binding, uint8_t *out);
int32_t ksf_eval_polynomial(const uint8_t *coeffs, size_t n_coeffs, uint16_t x, uint8_t *out);
int32_t ksf_eval_commitment(const uint8_t *cs, size_t n, uint16_t id, uint8_t *out);
int32_t ksf_pok_prove(const uint8_t *ctx, size_t ctx_len, uint16_t id, const uint8_t *a0,
	const uint8_t *rand, size_t rand_len, uint8_t *out);
int32_t ksf_pok_verify(const uint8_t *ctx, size_t ctx_len, uint16_t id, const uint8_t *c0,
	const uint8_t *proof, size_t proof_len);

int32_t ksf_test_panic(void);

#endif
