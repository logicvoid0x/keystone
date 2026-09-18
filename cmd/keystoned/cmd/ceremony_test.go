package cmd_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/cmd/keystoned/cmd"
	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
	poatypes "github.com/OWNER/keystone/x/poa/types"
)

func operatorInfos(t *testing.T, n int) []cmd.OperatorInfo {
	t.Helper()
	var out []cmd.OperatorInfo
	for i := 0; i < n; i++ {
		acc := secp256k1.GenPrivKeyFromSecret([]byte{'g', 'o', byte(i)})
		cons := ed25519.GenPrivKeyFromSecret([]byte{'g', 'c', byte(i)})
		enc, err := secret.RandomScalar(nil)
		require.NoError(t, err)
		ep, _ := frost.SerializePoint(frost.MulBase(&enc))
		hot := secp256k1.GenPrivKeyFromSecret([]byte{'g', 'h', byte(i)})
		out = append(out, cmd.OperatorInfo{
			Moniker: string(rune('a' + i)), OperatorAddress: sdk.AccAddress(acc.PubKey().Address()).String(),
			OperatorPubkey:  base64.StdEncoding.EncodeToString(acc.PubKey().Bytes()),
			ConsensusPubkey: base64.StdEncoding.EncodeToString(cons.PubKey().Bytes()),
			EncPubkey:       hex.EncodeToString(ep[:]), HotKey: sdk.AccAddress(hot.PubKey().Address()).String(),
		})
	}
	return out
}

// Spec M6-T4: 7 operators' public keys give a valid genesis with a 5-of-7 admin, the
// custody keys, no epoch, testnet params; the result does not depend on input order and
// a node starts from it with 7 validators.
func TestGenesisCeremony(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	infos := operatorInfos(t, 7)
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	res, err := cmd.Ceremony(a.AppCodec(), a.DefaultGenesis(), infos, "keystone-signet-1", at)
	require.NoError(t, err)
	require.Equal(t, 5, res.Threshold)

	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(res.Genesis.AppState, &state))
	require.NoError(t, a.BasicModuleManager.ValidateGenesis(a.AppCodec(), a.TxConfig(), state))
	var cg custodytypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(state[custodytypes.ModuleName], &cg)
	require.Empty(t, cg.Epochs, "no dealer: the DKG makes epoch 1")
	require.Len(t, cg.CustodyKeys, 7)
	require.Equal(t, custodytypes.NetworkSignet, cg.Params.BitcoinNetwork)
	require.Equal(t, uint32(6), cg.Params.ConfirmDepth)
	require.Equal(t, uint64(28800), cg.Params.MembershipDelayBlocks)
	var pg poatypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(state[poatypes.ModuleName], &pg)
	require.Equal(t, res.Admin.String(), pg.Params.Admin)

	// Order of the inputs does not matter.
	rev := append([]cmd.OperatorInfo(nil), infos...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	res2, err := cmd.Ceremony(a.AppCodec(), a.DefaultGenesis(), rev, "keystone-signet-1", at)
	require.NoError(t, err)
	require.Equal(t, string(res.Genesis.AppState), string(res2.Genesis.AppState))

	// A node starts from it.
	node := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID("keystone-signet-1"))
	cp := app.DefaultConsensusParams().ToProto()
	init, err := node.InitChain(&abci.RequestInitChain{ChainId: "keystone-signet-1", Time: at, InitialHeight: 1, AppStateBytes: res.Genesis.AppState, ConsensusParams: &cp})
	require.NoError(t, err)
	require.Len(t, init.Validators, 7)

	// Refused: localnet chain ids, a mismatched address, duplicates.
	_, err = cmd.Ceremony(a.AppCodec(), a.DefaultGenesis(), infos, "keystone-local-1", at)
	require.Error(t, err)
	bad := append([]cmd.OperatorInfo(nil), infos...)
	bad[0].OperatorAddress = infos[1].OperatorAddress
	_, err = cmd.Ceremony(a.AppCodec(), a.DefaultGenesis(), bad, "keystone-signet-1", at)
	require.ErrorContains(t, err, "does not match")
	_, err = cmd.Ceremony(a.AppCodec(), a.DefaultGenesis(), append(infos, infos[0]), "keystone-signet-1", at)
	require.ErrorContains(t, err, "twice")
}
