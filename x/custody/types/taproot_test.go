package types_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/v2"
	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/x/custody/types"
)

// bip341Vectors is the scriptPubKey part of BIP-341 wallet-test-vectors.json
// (testdata/, bitcoin/bips commit 100ba872c808946155b1eeb8c4568058f2eea0a7).
type bip341Vectors struct {
	ScriptPubKey []struct {
		Given struct {
			InternalPubkey string `json:"internalPubkey"`
		} `json:"given"`
		Intermediary struct {
			MerkleRoot    *string `json:"merkleRoot"`
			Tweak         string  `json:"tweak"`
			TweakedPubkey string  `json:"tweakedPubkey"`
		} `json:"intermediary"`
		Expected struct {
			ScriptPubKey  string `json:"scriptPubKey"`
			Bip350Address string `json:"bip350Address"`
		} `json:"expected"`
	} `json:"scriptPubKey"`
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// The key-path tweak matches every BIP-341 wallet vector (with and without a merkle
// root, which is where m_v goes).
func TestTapTweakBIP341Vectors(t *testing.T) {
	bz, err := os.ReadFile("testdata/bip341-wallet-test-vectors.json")
	require.NoError(t, err)
	var v bip341Vectors
	require.NoError(t, json.Unmarshal(bz, &v))
	require.Len(t, v.ScriptPubKey, 7)
	for i, c := range v.ScriptPubKey {
		p, err := schnorr.ParsePubKey(unhex(t, c.Given.InternalPubkey))
		require.NoError(t, err)
		var root []byte
		if c.Intermediary.MerkleRoot != nil {
			root = unhex(t, *c.Intermediary.MerkleRoot)
		}
		q, tweak, err := types.OutputKey(p, root)
		require.NoError(t, err, "vector %d", i)
		require.Equal(t, c.Intermediary.Tweak, hex.EncodeToString(tweak), "vector %d tweak", i)
		require.Equal(t, c.Intermediary.TweakedPubkey, hex.EncodeToString(schnorr.SerializePubKey(q)), "vector %d Q", i)
		script := types.P2TRScript(q)
		require.Equal(t, c.Expected.ScriptPubKey, hex.EncodeToString(script), "vector %d script", i)
		a, err := address.NewAddressTaproot(script[2:], &chaincfg.MainNetParams)
		require.NoError(t, err)
		require.Equal(t, c.Expected.Bip350Address, a.EncodeAddress(), "vector %d address", i)
	}
}

func testGroupKey(t *testing.T, odd bool) []byte {
	t.Helper()
	for i := byte(1); ; i++ {
		_, pub := btcec.PrivKeyFromBytes([]byte{i})
		bz := pub.SerializeCompressed()
		if (bz[0] == 0x03) == odd {
			return bz
		}
	}
}

func TestParseGroupKeyEvenY(t *testing.T) {
	for _, odd := range []bool{false, true} {
		bz := testGroupKey(t, odd)
		p, negated, err := types.ParseGroupKey(bz)
		require.NoError(t, err)
		require.Equal(t, odd, negated)
		require.Equal(t, byte(0x02), p.SerializeCompressed()[0])
		require.Equal(t, bz[1:], p.SerializeCompressed()[1:])
	}
}

// Vault scripts are distinct per (chain, epoch, vault), round-trip through bech32m on
// regtest and signet, and differ from the treasury script.
func TestVaultAddressRoundTrip(t *testing.T) {
	p, _, err := types.ParseGroupKey(testGroupKey(t, true))
	require.NoError(t, err)
	treasury, t0, err := types.TreasuryScript(p)
	require.NoError(t, err)
	require.Len(t, t0, 32)
	seen := map[string]bool{string(treasury): true}
	for _, c := range []struct {
		chain     string
		epoch, id uint64
	}{{"keystone-local-1", 1, 1}, {"keystone-local-1", 1, 2}, {"keystone-local-1", 2, 1}, {"keystone-local-2", 1, 1}} {
		script, tv, err := types.VaultScript(p, c.chain, c.epoch, c.id)
		require.NoError(t, err)
		require.True(t, types.IsP2TRScript(script))
		require.Len(t, tv, 32)
		require.False(t, seen[string(script)], "collision %+v", c)
		seen[string(script)] = true
		for _, network := range []string{types.NetworkRegtest, types.NetworkSignet} {
			addr, err := types.ScriptAddress(script, network)
			require.NoError(t, err)
			params, err := types.NetParams(network)
			require.NoError(t, err)
			dec, err := address.DecodeAddress(addr, params)
			require.NoError(t, err)
			require.True(t, dec.IsForNet(params))
			require.Equal(t, script[2:], dec.ScriptAddress())
		}
	}
	// Deterministic.
	a, _, err := types.VaultScript(p, "keystone-local-1", 1, 1)
	require.NoError(t, err)
	b, _, err := types.VaultScript(p, "keystone-local-1", 1, 1)
	require.NoError(t, err)
	require.Equal(t, a, b)
	_, err = types.ScriptAddress([]byte{0x51}, types.NetworkRegtest)
	require.Error(t, err)
	_, err = types.NetParams("mainnet")
	require.Error(t, err)
}
