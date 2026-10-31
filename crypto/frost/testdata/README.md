# FROST test vectors

Copied unchanged from the Zcash Foundation `frost` repository,
https://github.com/ZcashFoundation/frost, commit
`0966bd1529aa062ad3b621af99e277f976b1c0f0` (2026-07-21):

| File here | Source |
| --- | --- |
| `secp256k1-tr/vectors.json` | `frost-secp256k1-tr/tests/helpers/vectors.json` |
| `secp256k1-tr/vectors_dkg.json` | `frost-secp256k1-tr/tests/helpers/vectors_dkg.json` |
| `secp256k1-tr/vectors-big-identifier.json` | `frost-secp256k1-tr/tests/helpers/vectors-big-identifier.json` |

`vectors_test.go` replays frost-core's `check_sign_with_test_vectors` and
`check_dkg_keygen` on them, through the linked frost-secp256k1-tr 3.0.0 crate
(`crypto/frost/internal/zf`).
