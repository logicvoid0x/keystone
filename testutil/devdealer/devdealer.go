// Package devdealer is the localnet-only trusted dealer (spec M3, design §9.3 dev
// shortcut): one process generates the epoch-1 FROST key with crypto/frost trusted
// dealer keygen (spec M4a-T1) and hands each operator its share. Never used on a
// non-localnet chain id (see LocalChainID).
package devdealer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/btcsuite/btcd/btcec/v2"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// Epoch is the epoch the dealer creates.
const Epoch = 1

// LocalChainID reports whether chainID ends in -local-<n>, the only chain ids on which
// a dealer key (and a genesis custody epoch) is accepted.
func LocalChainID(chainID string) bool { return custodytypes.IsLocalChainID(chainID) }

// Threshold is t = floor(2n/3) + 1 (design §9.1).
func Threshold(n int) int { return 2*n/3 + 1 }

// Dealt is the dealer's output: genesis entries and one share file per operator.
type Dealt struct {
	Epoch           custodytypes.KeyEpoch
	ShareIndices    []custodytypes.ShareIndex
	VerifyingShares []custodytypes.VerifyingShare
	// Shares[i] belongs to Operators[i] (sorted by address bytes).
	Operators []sdk.AccAddress
	Shares    []secret.EpochShares
}

// Deal generates a t-of-n key for operators, using randomness from r. Share index =
// rank by operator address bytes, from 1. The key is drawn already even-y normalized
// (every coefficient negated if the group key had odd y, §9.6).
func Deal(r io.Reader, operators []sdk.AccAddress) (Dealt, error) {
	n := len(operators)
	if n == 0 {
		return Dealt{}, errors.New("no operators")
	}
	ops := slices.Clone(operators)
	slices.SortFunc(ops, func(a, b sdk.AccAddress) int { return bytes.Compare(a, b) })
	for i := 1; i < n; i++ {
		if bytes.Equal(ops[i-1], ops[i]) {
			return Dealt{}, fmt.Errorf("duplicate operator %s", ops[i])
		}
	}
	t := Threshold(n)
	gk, shares, err := secret.GenerateWithDealer(r, uint32(n), uint32(t), true)
	if err != nil {
		return Dealt{}, err
	}
	group := gk[:]
	d := Dealt{
		Epoch:     custodytypes.KeyEpoch{Number: Epoch, GroupKey: group, N: uint32(n), T: uint32(t), Status: custodytypes.EPOCH_STATUS_ACTIVE},
		Operators: ops,
	}
	for i, op := range ops {
		idx := uint32(i + 1)
		sb := shares[i].Secret.Bytes()
		yb, err := frost.SerializePoint(shares[i].VerifyingShare)
		if err != nil {
			return Dealt{}, err
		}
		y := yb[:]
		d.ShareIndices = append(d.ShareIndices, custodytypes.ShareIndex{Epoch: Epoch, Operator: op.String(), Index: idx})
		d.VerifyingShares = append(d.VerifyingShares, custodytypes.VerifyingShare{Epoch: Epoch, Index: idx, Pubkey: y})
		d.Shares = append(d.Shares, secret.EpochShares{
			Epoch: Epoch, GroupKey: group, N: uint32(n), T: uint32(t),
			Shares: []secret.Share{{Index: idx, Secret: sb[:], VerifyingShare: y}},
		})
	}
	return d, nil
}

// Apply puts the dealt epoch into a custody genesis as the ACTIVE epoch.
func (d Dealt) Apply(gs *custodytypes.GenesisState) {
	gs.Epochs = []custodytypes.KeyEpoch{d.Epoch}
	gs.ActiveEpoch = d.Epoch.Number
	gs.ShareIndices = slices.Clone(d.ShareIndices)
	gs.VerifyingShares = slices.Clone(d.VerifyingShares)
}

// Combine recovers the secret from shares (index → s_i) by Lagrange interpolation at
// 0. Test helper.
func Combine(shares map[uint32][]byte) (btcec.ModNScalar, error) {
	idx := make([]uint32, 0, len(shares))
	for i := range shares {
		idx = append(idx, i)
	}
	slices.Sort(idx)
	var sum btcec.ModNScalar
	for _, i := range idx {
		var si btcec.ModNScalar
		if si.SetByteSlice(shares[i]) {
			return sum, errors.New("share overflows")
		}
		num, den := new(btcec.ModNScalar).SetInt(1), new(btcec.ModNScalar).SetInt(1)
		for _, j := range idx {
			if j == i {
				continue
			}
			var xj, diff btcec.ModNScalar
			xj.SetInt(j)
			num.Mul(&xj)
			diff.SetInt(j)
			var xi btcec.ModNScalar
			xi.SetInt(i)
			diff.Add(xi.Negate())
			den.Mul(&diff)
		}
		den.InverseNonConst()
		si.Mul(num).Mul(den)
		sum.Add(&si)
	}
	return sum, nil
}
