package frost_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
)

type signVectors struct {
	Config struct {
		MaxParticipants string `json:"MAX_PARTICIPANTS"`
		MinParticipants string `json:"MIN_PARTICIPANTS"`
	} `json:"config"`
	Inputs struct {
		ParticipantList   []uint32 `json:"participant_list"`
		GroupSecretKey    string   `json:"group_secret_key"`
		VerifyingKey      string   `json:"verifying_key_key"`
		Message           string   `json:"message"`
		Coefficients      []string `json:"share_polynomial_coefficients"`
		ParticipantShares []struct {
			Identifier uint32 `json:"identifier"`
			Share      string `json:"participant_share"`
		} `json:"participant_shares"`
	} `json:"inputs"`
	RoundOne struct {
		Outputs []struct {
			Identifier         uint32 `json:"identifier"`
			HidingRandomness   string `json:"hiding_nonce_randomness"`
			BindingRandomness  string `json:"binding_nonce_randomness"`
			HidingNonce        string `json:"hiding_nonce"`
			BindingNonce       string `json:"binding_nonce"`
			HidingCommitment   string `json:"hiding_nonce_commitment"`
			BindingCommitment  string `json:"binding_nonce_commitment"`
			BindingFactorInput string `json:"binding_factor_input"`
			BindingFactor      string `json:"binding_factor"`
		} `json:"outputs"`
	} `json:"round_one_outputs"`
	RoundTwo struct {
		Outputs []struct {
			Identifier uint32 `json:"identifier"`
			SigShare   string `json:"sig_share"`
		} `json:"outputs"`
	} `json:"round_two_outputs"`
	Final struct {
		Sig string `json:"sig"`
	} `json:"final_output"`
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func scalar(t *testing.T, s string) frost.Scalar {
	t.Helper()
	v, err := frost.ParseScalar(unhex(t, s))
	require.NoError(t, err)
	return v
}

func ser(t *testing.T, p frost.Point) string {
	t.Helper()
	b, err := frost.SerializePoint(p)
	require.NoError(t, err)
	return hex.EncodeToString(b[:])
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	bz, err := os.ReadFile(filepath.Join("testdata", path))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bz, v))
}

// TestSignVectors runs frost-core's check_sign_with_test_vectors for both suites.
func TestSignVectors(t *testing.T) {
	for _, tc := range []struct {
		file  string
		suite frost.Suite
	}{
		{"secp256k1-tr/vectors.json", frost.Taproot},
		{"secp256k1-tr/vectors-big-identifier.json", frost.Taproot},
		{"secp256k1/vectors.json", frost.RFC9591},
		{"secp256k1/vectors-big-identifier.json", frost.RFC9591},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var v signVectors
			loadJSON(t, tc.file, &v)
			n, err := strconv.Atoi(v.Config.MaxParticipants)
			require.NoError(t, err)

			// Key generation: split the group secret with the given coefficients.
			sk := scalar(t, v.Inputs.GroupSecretKey)
			var coeffs []frost.Scalar
			for _, c := range v.Inputs.Coefficients {
				coeffs = append(coeffs, scalar(t, c))
			}
			shares, commits := secret.Split(sk, coeffs, uint32(n))
			require.Equal(t, v.Inputs.VerifyingKey, ser(t, commits[0]))
			byID := map[uint32]secret.DealtShare{}
			for _, s := range shares {
				byID[s.ID] = s
			}
			for _, ps := range v.Inputs.ParticipantShares {
				s := byID[ps.Identifier].Secret
				sb := s.Bytes()
				require.Equal(t, ps.Share, hex.EncodeToString(sb[:]), "share %d", ps.Identifier)
			}

			key, err := frost.NewKey(tc.suite, unhex(t, v.Inputs.VerifyingKey), nil)
			require.NoError(t, err)
			msg := unhex(t, v.Inputs.Message)

			// Round one: nonces from the given randomness, commitments.
			nonces := map[uint32]secret.Nonces{}
			var cs []frost.Commitment
			for _, o := range v.RoundOne.Outputs {
				share := byID[o.Identifier].Secret
				var hr, br [32]byte
				copy(hr[:], unhex(t, o.HidingRandomness))
				copy(br[:], unhex(t, o.BindingRandomness))
				n := secret.Nonces{
					Hiding:  secret.NonceGenerate(tc.suite, hr, share),
					Binding: secret.NonceGenerate(tc.suite, br, share),
				}
				hb, bb := n.Hiding.Bytes(), n.Binding.Bytes()
				require.Equal(t, o.HidingNonce, hex.EncodeToString(hb[:]))
				require.Equal(t, o.BindingNonce, hex.EncodeToString(bb[:]))
				d, e, err := n.Commitment()
				require.NoError(t, err)
				require.Equal(t, o.HidingCommitment, hex.EncodeToString(d[:]))
				require.Equal(t, o.BindingCommitment, hex.EncodeToString(e[:]))
				c, err := frost.ParseCommitment(o.Identifier, d[:], e[:])
				require.NoError(t, err)
				cs = append(cs, c)
				nonces[o.Identifier] = n
			}
			sg, err := frost.NewSigning(key, msg, cs)
			require.NoError(t, err)
			for _, o := range v.RoundOne.Outputs {
				in, err := sg.BindingFactorInput(o.Identifier)
				require.NoError(t, err)
				require.Equal(t, o.BindingFactorInput, hex.EncodeToString(in))
				rho, err := sg.BindingFactor(o.Identifier)
				require.NoError(t, err)
				rb := rho.Bytes()
				require.Equal(t, o.BindingFactor, hex.EncodeToString(rb[:]))
			}

			// Round two: shares, per-signer verification, aggregation.
			var zs []frost.Scalar
			for _, o := range v.RoundTwo.Outputs {
				s := byID[o.Identifier]
				z, err := secret.Sign(sg, o.Identifier, s.Secret, nonces[o.Identifier])
				require.NoError(t, err)
				zb := z.Bytes()
				require.Equal(t, o.SigShare, hex.EncodeToString(zb[:]), "share of %d", o.Identifier)
				require.True(t, sg.VerifyShares([]uint32{o.Identifier}, []frost.Point{s.VerifyingShare}, z))
				var bad frost.Scalar
				bad.SetInt(1)
				bad.Add(&z)
				require.False(t, sg.VerifyShares([]uint32{o.Identifier}, []frost.Point{s.VerifyingShare}, bad))
				zs = append(zs, z)
			}
			sig := sg.Aggregate(zs).Bytes(tc.suite)
			require.Equal(t, v.Final.Sig, hex.EncodeToString(sig))
			require.True(t, frost.Verify(key, msg, sig))
			sig[len(sig)-1] ^= 1
			require.False(t, frost.Verify(key, msg, sig))
		})
	}
}

type dkgParticipant struct {
	Identifier     uint32            `json:"identifier"`
	SigningKey     string            `json:"signing_key"`
	Coefficient    string            `json:"coefficient"`
	VSSCommitments []string          `json:"vss_commitments"`
	Proof          string            `json:"proof_of_knowledge"`
	SigningShares  map[string]string `json:"signing_shares"`
	VerifyingShare string            `json:"verifying_share"`
	SigningShare   string            `json:"signing_share"`
}

// TestDKGVectors runs frost-core's check_dkg_keygen: each participant checks the
// others' proofs of knowledge and shares, and derives its signing share, verifying
// share and the group key. For the Taproot suite, part3 applies post_dkg (even y,
// then the BIP-86 tweak t0 = H_TapTweak(x(P))), so the expected values are tweaked.
func TestDKGVectors(t *testing.T) {
	for _, tc := range []struct {
		file  string
		suite frost.Suite
	}{
		{"secp256k1-tr/vectors_dkg.json", frost.Taproot},
		{"secp256k1/vectors_dkg.json", frost.RFC9591},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var raw struct {
				Inputs map[string]json.RawMessage `json:"inputs"`
			}
			loadJSON(t, tc.file, &raw)
			var vk string
			require.NoError(t, json.Unmarshal(raw.Inputs["verifying_key"], &vk))
			parts := map[uint32]dkgParticipant{}
			for k, m := range raw.Inputs {
				if k == "verifying_key" {
					continue
				}
				var p dkgParticipant
				require.NoError(t, json.Unmarshal(m, &p))
				parts[p.Identifier] = p
			}
			require.Len(t, parts, 3)
			commits := map[uint32][]frost.Point{}
			for id, p := range parts {
				a0, a1 := scalar(t, p.SigningKey), scalar(t, p.Coefficient)
				_, cs := secret.Split(a0, []frost.Scalar{a1}, 0)
				for j, c := range p.VSSCommitments {
					require.Equal(t, c, ser(t, cs[j]))
				}
				commits[id] = cs
			}
			var group frost.Point
			for id := uint32(1); id <= 3; id++ {
				group = frost.AddPoints(group, commits[id][0])
			}
			for id := uint32(1); id <= 3; id++ {
				p := parts[id]
				a0, a1 := scalar(t, p.SigningKey), scalar(t, p.Coefficient)
				own, _ := secret.Split(a0, []frost.Scalar{a1}, id)
				s := own[id-1].Secret
				for other := uint32(1); other <= 3; other++ {
					if other == id {
						continue
					}
					require.True(t, frost.VerifyProofOfKnowledge(tc.suite, nil, other, commits[other][0], unhex(t, parts[other].Proof)))
					require.False(t, frost.VerifyProofOfKnowledge(tc.suite, nil, id, commits[other][0], unhex(t, parts[other].Proof)))
					f := scalar(t, p.SigningShares[strconv.Itoa(int(other))])
					require.True(t, frost.EqualPoints(frost.MulBase(&f), frost.EvalCommitment(commits[other], id)))
					s.Add(&f)
				}
				gk, err := frost.SerializePoint(group)
				require.NoError(t, err)
				if tc.suite.IsTaproot() {
					// post_dkg: P' = g_P·P, t0 = H_TapTweak(x(P)), share g_P·s + t0, key
					// P' + t0·G with whatever parity it has.
					p := group
					if gk[0] == 0x03 {
						s.Negate()
						p = frost.NegPoint(p)
					}
					t0, err := frost.ParseScalar(frost.TapTweak(group, nil))
					require.NoError(t, err)
					s.Add(&t0)
					require.Equal(t, vk, ser(t, frost.AddPoints(p, frost.MulBase(&t0))))
				} else {
					require.Equal(t, vk, hex.EncodeToString(gk[:]))
				}
				sb := s.Bytes()
				require.Equal(t, p.SigningShare, hex.EncodeToString(sb[:]))
				require.Equal(t, p.VerifyingShare, ser(t, frost.MulBase(&s)))
			}
		})
	}
}
