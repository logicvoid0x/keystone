// FLAG FOR AUDIT: the whole crate.

//! C ABI over the Zcash Foundation `frost-secp256k1-tr` / `frost-core` 3.0.0 crates for
//! Keystone's FROST operations (design §9.6, §9.11, §9.3). Every function is a thin
//! adapter: the cryptography is the ZF code, called through its public and `internals`
//! API. Two Keystone rules sit on top of it, both checked against the ZF entry points
//! in the tests below:
//!
//! * The Taproot tweak is given as the scalar t that Keystone stores (Utxo.tweak,
//!   SigningSession.tweak), not as a merkle root. `tweak_key_package` and
//!   `tweak_public_key_package` repeat frost-secp256k1-tr's `Tweak` impls with that t.
//! * The DKG proof of knowledge binds a context (chain_id ‖ epoch, design §13 A9):
//!   the challenge is HDKG(ctx ‖ id ‖ C_0 ‖ R). With an empty context it is frost-core's
//!   `compute_proof_of_knowledge` / `verify_proof_of_knowledge`, which are then used.
//!
//! The public functions (key, session check, share verification, aggregation,
//! verification, VSS evaluation, proof-of-knowledge verification) are consensus code:
//! they are deterministic, use no randomness and no I/O. The secret ones (nonces,
//! signing, dealer shares, proofs) take their randomness from the caller as bytes.
//!
//! Return codes: 0 ok (or valid), 1 invalid input or failed check, 2 internal panic
//! (caught; never unwinds into Go).

#![deny(unsafe_op_in_unsafe_fn)]

use std::collections::BTreeMap;
use std::panic::{catch_unwind, AssertUnwindSafe};

use frost_core::keys::dkg::{compute_proof_of_knowledge, verify_proof_of_knowledge};
use frost_core::keys::{
    CoefficientCommitment, KeyPackage, PublicKeyPackage, SigningShare,
    VerifiableSecretSharingCommitment, VerifyingShare,
};
use frost_core::round1::{GroupCommitmentShare, Nonce, NonceCommitment, SigningCommitments, SigningNonces};
use frost_core::round2::SignatureShare;
use frost_core::{
    aggregate_custom, compute_binding_factor_list, compute_group_commitment,
    derive_interpolating_value, BindingFactorList, Challenge, CheaterDetection, Ciphersuite,
    Field, Group, GroupCommitment, Identifier, Signature, SigningPackage, VerifyingKey,
};
use frost_secp256k1_tr::keys::EvenY;
use frost_secp256k1_tr::rand_core::{CryptoRng, RngCore};
use frost_secp256k1_tr::Secp256K1Sha256TR as S;

type G = <S as Ciphersuite>::Group;
type F = <G as Group>::Field;
type Scalar = frost_core::Scalar<S>;
type Element = frost_core::Element<S>;

const OK: i32 = 0;
const INVALID: i32 = 1;
const PANIC: i32 = 2;

/// Length of one encoded commitment: id (2 bytes BE) ‖ hiding (33) ‖ binding (33).
pub const COMMITMENT_LEN: usize = 68;
/// Length of a binding-factor preimage: vk (33) ‖ H4 (32) ‖ H5 (32) ‖ id (32).
pub const BINDING_INPUT_LEN: usize = 129;

type Res<T> = Result<T, ()>;

fn run(f: impl FnOnce() -> Res<()>) -> i32 {
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(Ok(())) => OK,
        Ok(Err(())) => INVALID,
        Err(_) => PANIC,
    }
}

/// # Safety
/// p must point to n readable bytes (or n == 0).
unsafe fn input<'a>(p: *const u8, n: usize) -> &'a [u8] {
    if n == 0 || p.is_null() {
        &[]
    } else {
        // SAFETY: the caller passes a buffer of n bytes.
        unsafe { std::slice::from_raw_parts(p, n) }
    }
}

/// # Safety
/// p must point to n writable bytes.
unsafe fn output<'a>(p: *mut u8, n: usize) -> &'a mut [u8] {
    // SAFETY: the caller passes a buffer of n bytes.
    unsafe { std::slice::from_raw_parts_mut(p, n) }
}

/// RNG that returns the caller's bytes, in order; running out is a bug (panic).
pub struct BytesRng<'a> {
    b: &'a [u8],
    pos: usize,
}

impl<'a> BytesRng<'a> {
    pub fn new(b: &'a [u8]) -> Self {
        BytesRng { b, pos: 0 }
    }
}

impl RngCore for BytesRng<'_> {
    fn next_u32(&mut self) -> u32 {
        let mut b = [0u8; 4];
        self.fill_bytes(&mut b);
        u32::from_le_bytes(b)
    }
    fn next_u64(&mut self) -> u64 {
        let mut b = [0u8; 8];
        self.fill_bytes(&mut b);
        u64::from_le_bytes(b)
    }
    fn fill_bytes(&mut self, dst: &mut [u8]) {
        let end = self.pos.checked_add(dst.len()).expect("overflow");
        assert!(end <= self.b.len(), "randomness exhausted");
        dst.copy_from_slice(&self.b[self.pos..end]);
        self.pos = end;
    }
    fn try_fill_bytes(&mut self, dst: &mut [u8]) -> Result<(), frost_secp256k1_tr::rand_core::Error> {
        self.fill_bytes(dst);
        Ok(())
    }
}

impl CryptoRng for BytesRng<'_> {}

// --- encodings -----------------------------------------------------------------------

fn scalar(b: &[u8]) -> Res<Scalar> {
    let ser = <F as Field>::Serialization::try_from(b).map_err(|_| ())?;
    <F as Field>::deserialize(&ser).map_err(|_| ())
}

fn element(b: &[u8]) -> Res<Element> {
    let ser = <G as Group>::Serialization::try_from(b).map_err(|_| ())?;
    <G as Group>::deserialize(&ser).map_err(|_| ())
}

fn put_scalar(s: &Scalar, out: &mut [u8]) {
    out.copy_from_slice(<F as Field>::serialize(s).as_ref());
}

fn put_element(e: &Element, out: &mut [u8]) -> Res<()> {
    let ser = <G as Group>::serialize(e).map_err(|_| ())?; // the identity has no encoding
    out.copy_from_slice(ser.as_ref());
    Ok(())
}

fn identifier(id: u16) -> Res<Identifier<S>> {
    Identifier::try_from(id).map_err(|_| ())
}

fn tweak(t: &[u8]) -> Res<Option<Scalar>> {
    if t.is_empty() {
        Ok(None)
    } else {
        scalar(t).map(Some)
    }
}

/// Commitments: records of COMMITMENT_LEN bytes, strictly increasing nonzero ids.
fn commitments(b: &[u8]) -> Res<BTreeMap<Identifier<S>, SigningCommitments<S>>> {
    if b.is_empty() || b.len() % COMMITMENT_LEN != 0 {
        return Err(());
    }
    let mut out = BTreeMap::new();
    let mut prev = 0u16;
    for rec in b.chunks(COMMITMENT_LEN) {
        let id = u16::from_be_bytes([rec[0], rec[1]]);
        if id <= prev {
            return Err(());
        }
        prev = id;
        let hiding = NonceCommitment::<S>::deserialize(&rec[2..35]).map_err(|_| ())?;
        let binding = NonceCommitment::<S>::deserialize(&rec[35..68]).map_err(|_| ())?;
        out.insert(identifier(id)?, SigningCommitments::new(hiding, binding));
    }
    Ok(out)
}

// --- Taproot tweak by scalar ---------------------------------------------------------

/// frost-secp256k1-tr `Tweak for PublicKeyPackage` with the tweak scalar t given.
fn tweak_public_key_package(p: PublicKeyPackage<S>, t: Option<Scalar>) -> PublicKeyPackage<S> {
    let Some(t) = t else { return p };
    let tp = G::generator() * t;
    let p = p.into_even_y(None);
    let vk = VerifyingKey::new(p.verifying_key().to_element() + tp);
    let shares: BTreeMap<_, _> = p
        .verifying_shares()
        .iter()
        .map(|(i, vs)| (*i, VerifyingShare::new(vs.to_element() + tp)))
        .collect();
    PublicKeyPackage::new(shares, vk, p.min_signers())
}

/// frost-secp256k1-tr `Tweak for KeyPackage` with the tweak scalar t given.
fn tweak_key_package(k: KeyPackage<S>, t: Option<Scalar>) -> KeyPackage<S> {
    let Some(t) = t else { return k };
    let tp = G::generator() * t;
    let k = k.into_even_y(None);
    KeyPackage::new(
        *k.identifier(),
        SigningShare::new(k.signing_share().to_scalar() + t),
        VerifyingShare::new(k.verifying_share().to_element() + tp),
        VerifyingKey::new(k.verifying_key().to_element() + tp),
        *k.min_signers(),
    )
}

// --- signing sessions ----------------------------------------------------------------

/// The public state of a signing session, prepared exactly as frost-core
/// `aggregate_custom` / `verify_signature_share` do.
struct Prepared {
    sp: SigningPackage<S>,
    pkp: PublicKeyPackage<S>,
    bfl: BindingFactorList<S>,
    gc: GroupCommitment<S>,
    c: Challenge<S>,
}

struct Session<'a> {
    group_key: Element,
    tweak: Option<Scalar>,
    msg: &'a [u8],
    commitments: BTreeMap<Identifier<S>, SigningCommitments<S>>,
}

impl Session<'_> {
    fn signing_package(&self) -> SigningPackage<S> {
        SigningPackage::new(self.commitments.clone(), self.msg)
    }

    fn public_key_package(&self, shares: BTreeMap<Identifier<S>, VerifyingShare<S>>) -> PublicKeyPackage<S> {
        let p = PublicKeyPackage::new(shares, VerifyingKey::new(self.group_key), None);
        tweak_public_key_package(p, self.tweak)
    }

    fn prepare(&self, shares: BTreeMap<Identifier<S>, VerifyingShare<S>>) -> Res<Prepared> {
        let sp = self.signing_package();
        let pkp = self.public_key_package(shares);
        let none = BTreeMap::new();
        let (sp, _, pkp) = S::pre_aggregate(&sp, &none, &pkp).map_err(|_| ())?;
        let (sp, pkp) = (sp.into_owned(), pkp.into_owned());
        let bfl = compute_binding_factor_list(&sp, pkp.verifying_key(), &[]).map_err(|_| ())?;
        let sp = S::pre_commitment_aggregate(&sp, &bfl).map_err(|_| ())?.into_owned();
        let gc = compute_group_commitment(&sp, &bfl).map_err(|_| ())?;
        if gc.clone().to_element() == G::identity() {
            return Err(());
        }
        let c = S::challenge(&gc.clone().to_element(), pkp.verifying_key(), sp.message()).map_err(|_| ())?;
        Ok(Prepared { sp, pkp, bfl, gc, c })
    }
}

/// # Safety
/// Buffers as in keystone_frost.h.
unsafe fn session<'a>(
    gk: *const u8,
    tw: *const u8,
    tw_len: usize,
    msg: *const u8,
    msg_len: usize,
    cs: *const u8,
    cs_len: usize,
) -> Res<Session<'a>> {
    // SAFETY: forwarded from the caller.
    unsafe {
        Ok(Session {
            group_key: element(input(gk, 33))?,
            tweak: tweak(input(tw, tw_len))?,
            msg: input(msg, msg_len),
            commitments: commitments(input(cs, cs_len))?,
        })
    }
}

/// The effective verifying key x(Q) as 02 ‖ x(Q) for group key P and optional tweak t.
///
/// # Safety
/// gk: 33 bytes; tw: tw_len (0 or 32) bytes; out: 33 bytes.
#[no_mangle]
pub unsafe extern "C" fn ksf_key(gk: *const u8, tw: *const u8, tw_len: usize, out: *mut u8) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (gk, tw, out) = unsafe { (input(gk, 33), input(tw, tw_len), output(out, 33)) };
        let p = PublicKeyPackage::new(BTreeMap::new(), VerifyingKey::new(element(gk)?), None);
        let vk = tweak_public_key_package(p, tweak(tw)?).verifying_key().into_even_y(None);
        put_element(&vk.to_element(), out)
    })
}

/// Checks that a session can be signed: well-formed commitments and a group commitment
/// that is not the identity.
///
/// # Safety
/// Buffers as in keystone_frost.h.
#[no_mangle]
pub unsafe extern "C" fn ksf_session_check(
    gk: *const u8, tw: *const u8, tw_len: usize, msg: *const u8, msg_len: usize, cs: *const u8, cs_len: usize,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let s = unsafe { session(gk, tw, tw_len, msg, msg_len, cs, cs_len) }?;
        s.prepare(BTreeMap::new()).map(|_| ())
    })
}

/// Signer id's binding-factor preimage (BINDING_INPUT_LEN bytes) and binding factor.
///
/// # Safety
/// Buffers as in keystone_frost.h.
#[no_mangle]
pub unsafe extern "C" fn ksf_binding_factor(
    gk: *const u8, tw: *const u8, tw_len: usize, msg: *const u8, msg_len: usize, cs: *const u8, cs_len: usize,
    id: u16, out_input: *mut u8, out_rho: *mut u8,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (s, out_input, out_rho) = unsafe {
            (session(gk, tw, tw_len, msg, msg_len, cs, cs_len)?, output(out_input, BINDING_INPUT_LEN), output(out_rho, 32))
        };
        let p = s.prepare(BTreeMap::new())?;
        let id = identifier(id)?;
        let pre = p.sp.binding_factor_preimages(p.pkp.verifying_key(), &[]).map_err(|_| ())?;
        let (_, input) = pre.iter().find(|(i, _)| *i == id).ok_or(())?;
        if input.len() != BINDING_INPUT_LEN {
            return Err(());
        }
        out_input.copy_from_slice(input);
        put_scalar(&S::H1(input), out_rho);
        Ok(())
    })
}

/// Verifies the combined share z = Σ z_i of signers ids (n_ids × 2 bytes BE) against
/// their raw verifying shares ys (n_ids × 33 bytes, same order). One signer is frost-core
/// `verify_signature_share`; several are checked as one linear combination with the same
/// Taproot `verify_share`: z·G == Σ R_i + c·Σ λ_i·Y_i (R_i negated with the group
/// commitment). Returns 0 when valid.
///
/// # Safety
/// Buffers as in keystone_frost.h.
#[no_mangle]
pub unsafe extern "C" fn ksf_verify_shares(
    gk: *const u8, tw: *const u8, tw_len: usize, msg: *const u8, msg_len: usize, cs: *const u8, cs_len: usize,
    ids: *const u8, ys: *const u8, n_ids: usize, z: *const u8,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (s, ids, ys, z) = unsafe {
            (
                session(gk, tw, tw_len, msg, msg_len, cs, cs_len)?,
                input(ids, n_ids.checked_mul(2).ok_or(())?),
                input(ys, n_ids.checked_mul(33).ok_or(())?),
                input(z, 32),
            )
        };
        if n_ids == 0 {
            return Err(());
        }
        let mut signers = Vec::with_capacity(n_ids);
        let mut shares = BTreeMap::new();
        for k in 0..n_ids {
            let id = identifier(u16::from_be_bytes([ids[2 * k], ids[2 * k + 1]]))?;
            if !s.commitments.contains_key(&id) || shares.contains_key(&id) {
                return Err(());
            }
            shares.insert(id, VerifyingShare::new(element(&ys[33 * k..33 * k + 33])?));
            signers.push(id);
        }
        let share = SignatureShare::<S>::deserialize(z).map_err(|_| ())?;
        let p = s.prepare(shares)?;
        let mut r = G::identity();
        let mut y = G::identity();
        let mut lambda1 = F::one();
        for id in &signers {
            let lambda = derive_interpolating_value(id, &p.sp).map_err(|_| ())?;
            let rho = p.bfl.get(id).ok_or(())?;
            let rs = p.sp.signing_commitment(id).ok_or(())?.to_group_commitment_share(rho);
            let ys = p.pkp.verifying_shares().get(id).ok_or(())?;
            r = r + rs.to_element();
            y = y + ys.to_element() * lambda;
            lambda1 = lambda;
        }
        if signers.len() == 1 {
            let id = signers[0];
            let rs = p.sp.signing_commitment(&id).ok_or(())?.to_group_commitment_share(p.bfl.get(&id).ok_or(())?);
            let ys = p.pkp.verifying_shares().get(&id).ok_or(())?;
            return S::verify_share(&p.gc, &share, id, &rs, ys, lambda1, &p.c).map_err(|_| ());
        }
        S::verify_share(
            &p.gc,
            &share,
            signers[0],
            &GroupCommitmentShare::from_element(r),
            &VerifyingShare::new(y),
            F::one(),
            &p.c,
        )
        .map_err(|_| ())
    })
}

/// Aggregates the session's signature from shares whose sum is the signature's z (n_zs ×
/// 32 bytes, at most one per signer; combined shares are fine) with frost-core
/// `aggregate_custom` (no cheater detection: shares are verified on their own), which
/// checks the result. out: the 64-byte BIP-340 signature.
///
/// # Safety
/// Buffers as in keystone_frost.h.
#[no_mangle]
pub unsafe extern "C" fn ksf_aggregate(
    gk: *const u8, tw: *const u8, tw_len: usize, msg: *const u8, msg_len: usize, cs: *const u8, cs_len: usize,
    zs: *const u8, n_zs: usize, out: *mut u8,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (s, zs, out) = unsafe {
            (session(gk, tw, tw_len, msg, msg_len, cs, cs_len)?, input(zs, n_zs.checked_mul(32).ok_or(())?), output(out, 64))
        };
        if n_zs == 0 || n_zs > s.commitments.len() {
            return Err(());
        }
        let zero = [0u8; 32];
        let mut shares = BTreeMap::new();
        for (k, id) in s.commitments.keys().enumerate() {
            let z = if k < n_zs { &zs[32 * k..32 * k + 32] } else { &zero[..] };
            shares.insert(*id, SignatureShare::<S>::deserialize(z).map_err(|_| ())?);
        }
        let sig = aggregate_custom(&s.signing_package(), &shares, &s.public_key_package(BTreeMap::new()), CheaterDetection::Disabled)
            .map_err(|_| ())?;
        let bytes = sig.serialize().map_err(|_| ())?;
        if bytes.len() != 64 {
            return Err(());
        }
        out.copy_from_slice(&bytes);
        Ok(())
    })
}

/// BIP-340 verification of sig (64 bytes) on msg under vk (33 bytes) with frost-core
/// `VerifyingKey::verify`. Returns 0 when valid.
///
/// # Safety
/// Buffers as in keystone_frost.h.
#[no_mangle]
pub unsafe extern "C" fn ksf_verify(vk: *const u8, msg: *const u8, msg_len: usize, sig: *const u8, sig_len: usize) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (vk, msg, sig) = unsafe { (input(vk, 33), input(msg, msg_len), input(sig, sig_len)) };
        let vk = VerifyingKey::<S>::deserialize(vk).map_err(|_| ())?;
        let sig = Signature::<S>::deserialize(sig).map_err(|_| ())?;
        vk.verify(msg, &sig).map_err(|_| ())
    })
}

// --- secret operations (sidecars only) -----------------------------------------------

/// RFC 9591 nonce_generate(random, share) = H3(random ‖ share), via frost-core
/// `Nonce::new` fed with the 32 random bytes.
///
/// # Safety
/// random, share, out: 32 bytes each.
#[no_mangle]
pub unsafe extern "C" fn ksf_nonce_generate(random: *const u8, share: *const u8, out: *mut u8) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (random, share, out) = unsafe { (input(random, 32), input(share, 32), output(out, 32)) };
        let share = SigningShare::<S>::deserialize(share).map_err(|_| ())?;
        let n = Nonce::<S>::new(&share, &mut BytesRng::new(random));
        out.copy_from_slice(&n.serialize());
        Ok(())
    })
}

/// The commitments (D, E) of nonces (d, e).
///
/// # Safety
/// hiding, binding: 32 bytes; out_d, out_e: 33 bytes.
#[no_mangle]
pub unsafe extern "C" fn ksf_nonce_commit(hiding: *const u8, binding: *const u8, out_d: *mut u8, out_e: *mut u8) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (h, b, od, oe) = unsafe { (input(hiding, 32), input(binding, 32), output(out_d, 33), output(out_e, 33)) };
        let n = SigningNonces::from_nonces(
            Nonce::<S>::deserialize(h).map_err(|_| ())?,
            Nonce::<S>::deserialize(b).map_err(|_| ())?,
        );
        let c = n.commitments();
        od.copy_from_slice(&c.hiding().serialize().map_err(|_| ())?);
        oe.copy_from_slice(&c.binding().serialize().map_err(|_| ())?);
        Ok(())
    })
}

/// Signer id's signature share with frost-secp256k1-tr `round2::sign` for the raw
/// share s of group key P (the session's tweak applied as in `sign_with_tweak`) and
/// nonces (d, e), which must match id's commitment.
///
/// # Safety
/// Buffers as in keystone_frost.h.
#[no_mangle]
pub unsafe extern "C" fn ksf_sign(
    gk: *const u8, tw: *const u8, tw_len: usize, msg: *const u8, msg_len: usize, cs: *const u8, cs_len: usize,
    id: u16, share: *const u8, hiding: *const u8, binding: *const u8, out: *mut u8,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (s, share, h, b, out) = unsafe {
            (session(gk, tw, tw_len, msg, msg_len, cs, cs_len)?, input(share, 32), input(hiding, 32), input(binding, 32), output(out, 32))
        };
        let id = identifier(id)?;
        let secret = scalar(share)?;
        let min = u16::try_from(s.commitments.len()).map_err(|_| ())?;
        let kp = KeyPackage::new(
            id,
            SigningShare::new(secret),
            VerifyingShare::new(G::generator() * secret),
            VerifyingKey::new(s.group_key),
            min,
        );
        let kp = tweak_key_package(kp, s.tweak);
        let nonces = SigningNonces::from_nonces(
            Nonce::<S>::deserialize(h).map_err(|_| ())?,
            Nonce::<S>::deserialize(b).map_err(|_| ())?,
        );
        let z = frost_secp256k1_tr::round2::sign(&s.signing_package(), &nonces, &kp).map_err(|_| ())?;
        out.copy_from_slice(&z.serialize());
        Ok(())
    })
}

fn coefficients(b: &[u8]) -> Res<Vec<Scalar>> {
    if b.is_empty() || b.len() % 32 != 0 {
        return Err(());
    }
    b.chunks(32).map(scalar).collect()
}

/// f(x) for the polynomial with coefficients a_0..a_{t−1} (n_coeffs × 32 bytes), with
/// frost-core `SigningShare::from_coefficients`.
///
/// # Safety
/// coeffs: n_coeffs × 32 bytes; out: 32 bytes.
#[no_mangle]
pub unsafe extern "C" fn ksf_eval_polynomial(coeffs: *const u8, n_coeffs: usize, x: u16, out: *mut u8) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (c, out) = unsafe { (input(coeffs, n_coeffs.checked_mul(32).ok_or(())?), output(out, 32)) };
        let s = SigningShare::<S>::from_coefficients(&coefficients(c)?, identifier(x)?);
        out.copy_from_slice(&s.serialize());
        Ok(())
    })
}

/// Σ_j id^j·C_j for commitments C_0..C_{t−1} (n × 33 bytes), with frost-core
/// `VerifyingShare::from_commitment` (VSS evaluation; also a participant's verifying
/// share from the summed DKG commitments).
///
/// # Safety
/// cs: n × 33 bytes; out: 33 bytes.
#[no_mangle]
pub unsafe extern "C" fn ksf_eval_commitment(cs: *const u8, n: usize, id: u16, out: *mut u8) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (cs, out) = unsafe { (input(cs, n.checked_mul(33).ok_or(())?), output(out, 33)) };
        if cs.is_empty() {
            return Err(());
        }
        let vss = VerifiableSecretSharingCommitment::<S>::deserialize(cs.chunks(33).collect::<Vec<_>>()).map_err(|_| ())?;
        let y = VerifyingShare::from_commitment(identifier(id)?, &vss);
        put_element(&y.to_element(), out)
    })
}

/// HDKG(ctx ‖ id ‖ C_0 ‖ R): frost-core's proof-of-knowledge challenge with Keystone's
/// context prefix (empty: frost-core's own).
fn pok_challenge(ctx: &[u8], id: Identifier<S>, c0: &Element, r: &Element) -> Res<Scalar> {
    let mut pre = ctx.to_vec();
    pre.extend_from_slice(id.serialize().as_ref());
    pre.extend_from_slice(<G as Group>::serialize(c0).map_err(|_| ())?.as_ref());
    pre.extend_from_slice(<G as Group>::serialize(r).map_err(|_| ())?.as_ref());
    S::HDKG(&pre).ok_or(())
}

fn commitment_of(a0: Scalar) -> VerifiableSecretSharingCommitment<S> {
    VerifiableSecretSharingCommitment::new(vec![CoefficientCommitment::new(G::generator() * a0)])
}

/// A dealer's proof of knowledge of a_0 (64 bytes, x-only R ‖ μ): frost-core
/// `compute_proof_of_knowledge` (nonce from frost-secp256k1-tr `generate_nonce`, even R)
/// with the challenge over ctx. rand feeds the nonce (at least 32 bytes; more in case
/// of rejection sampling).
///
/// # Safety
/// ctx: ctx_len bytes; a0: 32; rand: rand_len; out: 64.
#[no_mangle]
pub unsafe extern "C" fn ksf_pok_prove(
    ctx: *const u8, ctx_len: usize, id: u16, a0: *const u8, rand: *const u8, rand_len: usize, out: *mut u8,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (ctx, a0, rand, out) = unsafe { (input(ctx, ctx_len), input(a0, 32), input(rand, rand_len), output(out, 64)) };
        let id = identifier(id)?;
        let a0 = scalar(a0)?;
        let mut rng = BytesRng::new(rand);
        let sig = if ctx.is_empty() {
            compute_proof_of_knowledge(id, &[a0], &commitment_of(a0), &mut rng).map_err(|_| ())?
        } else {
            let (k, r) = S::generate_nonce(&mut rng);
            let c = pok_challenge(ctx, id, &(G::generator() * a0), &r)?;
            Signature::new(r, k + a0 * c)
        };
        let bytes = sig.serialize().map_err(|_| ())?;
        out.copy_from_slice(&bytes);
        Ok(())
    })
}

/// Verifies a dealer's proof σ = (R, μ) of a_0 for C_0: R == μ·G − c·C_0 (frost-core
/// `verify_proof_of_knowledge` when ctx is empty). Returns 0 when valid.
///
/// # Safety
/// ctx: ctx_len bytes; c0: 33; proof: proof_len.
#[no_mangle]
pub unsafe extern "C" fn ksf_pok_verify(
    ctx: *const u8, ctx_len: usize, id: u16, c0: *const u8, proof: *const u8, proof_len: usize,
) -> i32 {
    run(|| {
        // SAFETY: buffers as documented.
        let (ctx, c0, proof) = unsafe { (input(ctx, ctx_len), input(c0, 33), input(proof, proof_len)) };
        let id = identifier(id)?;
        let c0 = element(c0)?;
        let sig = Signature::<S>::deserialize(proof).map_err(|_| ())?;
        if ctx.is_empty() {
            let vss = VerifiableSecretSharingCommitment::new(vec![CoefficientCommitment::new(c0)]);
            return verify_proof_of_knowledge(id, &vss, &sig).map_err(|_| ());
        }
        let c = pok_challenge(ctx, id, &c0, sig.R())?;
        if *sig.R() != G::generator() * *sig.z() - c0 * c {
            return Err(());
        }
        Ok(())
    })
}

/// Test hook: panics inside the guard, so the Go side can check that a panic comes
/// back as code 2 and never unwinds into Go.
#[no_mangle]
pub extern "C" fn ksf_test_panic() -> i32 {
    run(|| {
        let v: Vec<u8> = Vec::new();
        let i = v.len() + 1;
        if v[i] == 0 {
            return Err(());
        }
        Ok(())
    })
}

#[cfg(test)]
mod tests;
