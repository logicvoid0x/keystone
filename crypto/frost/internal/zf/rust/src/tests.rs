//! Checks the C ABI against the ZF entry points it adapts: signing and aggregation with
//! the tweak scalar t = H_TapTweak(x(P) ‖ root) match `sign_with_tweak` /
//! `aggregate_with_tweak(root)` byte for byte, untweaked ones match `sign` /
//! `aggregate`, and the empty-context proof of knowledge is frost-core's.

use super::*;
use frost_secp256k1_tr as tr;
use sha2::{Digest, Sha256};

/// Deterministic test RNG (xorshift64*); not for production.
struct TestRng(u64);

impl RngCore for TestRng {
    fn next_u32(&mut self) -> u32 {
        self.next_u64() as u32
    }
    fn next_u64(&mut self) -> u64 {
        self.0 ^= self.0 >> 12;
        self.0 ^= self.0 << 25;
        self.0 ^= self.0 >> 27;
        self.0.wrapping_mul(0x2545_f491_4f6c_dd1d)
    }
    fn fill_bytes(&mut self, dst: &mut [u8]) {
        for b in dst.chunks_mut(8) {
            let v = self.next_u64().to_le_bytes();
            b.copy_from_slice(&v[..b.len()]);
        }
    }
    fn try_fill_bytes(&mut self, dst: &mut [u8]) -> Result<(), tr::rand_core::Error> {
        self.fill_bytes(dst);
        Ok(())
    }
}

impl CryptoRng for TestRng {}

fn tap_tweak(p: &Element, root: &[u8]) -> [u8; 32] {
    let tag = Sha256::digest(b"TapTweak");
    let mut h = Sha256::new();
    h.update(tag);
    h.update(tag);
    h.update(&<G as Group>::serialize(p).unwrap()[1..]);
    h.update(root);
    h.finalize().into()
}

fn ser_e(e: &Element) -> Vec<u8> {
    <G as Group>::serialize(e).unwrap().as_ref().to_vec()
}

fn encode_commitments(cs: &BTreeMap<Identifier<S>, SigningCommitments<S>>, ids: &[u16]) -> Vec<u8> {
    let mut out = Vec::new();
    for (id, c) in ids.iter().zip(cs.values()) {
        out.extend_from_slice(&id.to_be_bytes());
        out.extend_from_slice(&c.hiding().serialize().unwrap());
        out.extend_from_slice(&c.binding().serialize().unwrap());
    }
    out
}

#[test]
fn matches_zf_sign_and_aggregate() {
    let mut rng = TestRng(0x9e37_79b9_7f4a_7c15);
    for iter in 0..24u32 {
        let (secret_shares, pkp) = tr::keys::generate_with_dealer(4, 3, tr::keys::IdentifierList::Default, &mut rng).unwrap();
        let gk = ser_e(&pkp.verifying_key().to_element());
        let ids: Vec<u16> = if iter % 2 == 0 { vec![1, 2, 3] } else { vec![1, 2, 3, 4] };
        let root: Option<Vec<u8>> = match iter % 3 {
            0 => None,
            1 => Some(Vec::new()),
            _ => Some((0..32).map(|i| (i as u8) ^ (iter as u8)).collect()),
        };
        let msg = [iter as u8; 32];

        let mut nonces = BTreeMap::new();
        let mut comms = BTreeMap::new();
        for id in &ids {
            let ident = identifier(*id).unwrap();
            let kp = tr::keys::KeyPackage::try_from(secret_shares[&ident].clone()).unwrap();
            let (n, c) = tr::round1::commit(kp.signing_share(), &mut rng);
            nonces.insert(ident, (n, kp));
            comms.insert(ident, c);
        }
        let sp = tr::SigningPackage::new(comms.clone(), &msg);
        let cs = encode_commitments(&comms, &ids);

        // Keystone stores t (None: untweaked; Some(root): t = H_TapTweak(x(P) ‖ root)).
        let t = root.as_ref().map(|r| tap_tweak(&pkp.verifying_key().to_element(), r));
        let tw: &[u8] = t.as_ref().map(|t| &t[..]).unwrap_or(&[]);

        let mut shares = BTreeMap::new();
        let mut zs = Vec::new();
        for id in &ids {
            let ident = identifier(*id).unwrap();
            let (n, kp) = &nonces[&ident];
            let want = match &root {
                None => tr::round2::sign(&sp, n, kp).unwrap(),
                Some(r) => tr::round2::sign_with_tweak(&sp, n, kp, Some(r)).unwrap(),
            };
            let mut z = [0u8; 32];
            let share = kp.signing_share().serialize();
            let rc = unsafe {
                ksf_sign(
                    gk.as_ptr(), tw.as_ptr(), tw.len(), msg.as_ptr(), msg.len(), cs.as_ptr(), cs.len(), *id,
                    share.as_ptr(), n.hiding().serialize().as_ptr(), n.binding().serialize().as_ptr(), z.as_mut_ptr(),
                )
            };
            assert_eq!(rc, OK);
            assert_eq!(z.to_vec(), want.serialize(), "iter {iter} share {id}");

            let y = ser_e(&kp.verifying_share().to_element());
            let idb = id.to_be_bytes();
            let ok = unsafe {
                ksf_verify_shares(gk.as_ptr(), tw.as_ptr(), tw.len(), msg.as_ptr(), msg.len(), cs.as_ptr(), cs.len(), idb.as_ptr(), y.as_ptr(), 1, z.as_ptr())
            };
            assert_eq!(ok, OK);
            let mut bad = z;
            bad[31] ^= 1;
            let ok = unsafe {
                ksf_verify_shares(gk.as_ptr(), tw.as_ptr(), tw.len(), msg.as_ptr(), msg.len(), cs.as_ptr(), cs.len(), idb.as_ptr(), y.as_ptr(), 1, bad.as_ptr())
            };
            assert_eq!(ok, INVALID);
            shares.insert(ident, want);
            zs.push(z);
        }

        // Combined share of the first two signers.
        let z01 = scalar(&zs[0]).unwrap() + scalar(&zs[1]).unwrap();
        let z01 = <F as Field>::serialize(&z01);
        let mut ids01 = Vec::new();
        let mut ys01 = Vec::new();
        for id in &ids[..2] {
            ids01.extend_from_slice(&id.to_be_bytes());
            ys01.extend_from_slice(&ser_e(&nonces[&identifier(*id).unwrap()].1.verifying_share().to_element()));
        }
        let ok = unsafe {
            ksf_verify_shares(gk.as_ptr(), tw.as_ptr(), tw.len(), msg.as_ptr(), msg.len(), cs.as_ptr(), cs.len(), ids01.as_ptr(), ys01.as_ptr(), 2, z01.as_ptr())
        };
        assert_eq!(ok, OK);
        let mut ys10 = ys01[33..].to_vec();
        ys10.extend_from_slice(&ys01[..33]);
        let ok = unsafe {
            ksf_verify_shares(gk.as_ptr(), tw.as_ptr(), tw.len(), msg.as_ptr(), msg.len(), cs.as_ptr(), cs.len(), ids01.as_ptr(), ys10.as_ptr(), 2, z01.as_ptr())
        };
        assert_eq!(ok, INVALID, "shares swapped between signers");

        let want = match &root {
            None => tr::aggregate(&sp, &shares, &pkp).unwrap(),
            Some(r) => tr::aggregate_with_tweak(&sp, &shares, &pkp, Some(r)).unwrap(),
        };
        let mut flat = Vec::new();
        for z in &zs {
            flat.extend_from_slice(z);
        }
        let mut sig = [0u8; 64];
        let rc = unsafe {
            ksf_aggregate(gk.as_ptr(), tw.as_ptr(), tw.len(), msg.as_ptr(), msg.len(), cs.as_ptr(), cs.len(), flat.as_ptr(), zs.len(), sig.as_mut_ptr())
        };
        assert_eq!(rc, OK);
        assert_eq!(sig.to_vec(), want.serialize().unwrap());

        let mut vk = [0u8; 33];
        assert_eq!(unsafe { ksf_key(gk.as_ptr(), tw.as_ptr(), tw.len(), vk.as_mut_ptr()) }, OK);
        let want_vk = match &root {
            None => pkp.clone(),
            Some(r) => tr::keys::Tweak::tweak(pkp.clone(), Some(r)),
        };
        assert_eq!(vk[1..], ser_e(&want_vk.verifying_key().to_element())[1..]);
        assert_eq!(unsafe { ksf_verify(vk.as_ptr(), msg.as_ptr(), msg.len(), sig.as_ptr(), 64) }, OK);
        sig[63] ^= 1;
        assert_eq!(unsafe { ksf_verify(vk.as_ptr(), msg.as_ptr(), msg.len(), sig.as_ptr(), 64) }, INVALID);
    }
}

#[test]
fn proof_of_knowledge_context() {
    let a0 = scalar(&[7u8; 32]).unwrap();
    let c0 = ser_e(&(G::generator() * a0));
    let a0b = <F as Field>::serialize(&a0);
    let rand = [0x42u8; 128];
    for ctx in [&b""[..], &b"keystone-local-1\x00\x00\x00\x00\x00\x00\x00\x01"[..]] {
        let mut proof = [0u8; 64];
        assert_eq!(unsafe { ksf_pok_prove(ctx.as_ptr(), ctx.len(), 3, a0b.as_ptr(), rand.as_ptr(), rand.len(), proof.as_mut_ptr()) }, OK);
        assert_eq!(unsafe { ksf_pok_verify(ctx.as_ptr(), ctx.len(), 3, c0.as_ptr(), proof.as_ptr(), 64) }, OK);
        assert_eq!(unsafe { ksf_pok_verify(ctx.as_ptr(), ctx.len(), 2, c0.as_ptr(), proof.as_ptr(), 64) }, INVALID);

        // The context path with an empty context is frost-core's proof.
        let mut rng = BytesRng::new(&rand);
        let (k, r) = S::generate_nonce(&mut rng);
        let c = pok_challenge(ctx, identifier(3).unwrap(), &(G::generator() * a0), &r).unwrap();
        assert_eq!(Signature::<S>::new(r, k + a0 * c).serialize().unwrap(), proof.to_vec());
    }
}

#[test]
fn panics_are_caught() {
    assert_eq!(ksf_test_panic(), PANIC);
    let mut out = [0u8; 32];
    assert_eq!(unsafe { ksf_nonce_generate([1u8; 32].as_ptr(), [0xffu8; 32].as_ptr(), out.as_mut_ptr()) }, INVALID);
}
