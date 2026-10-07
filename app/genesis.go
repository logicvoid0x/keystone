package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	admintypes "github.com/OWNER/keystone/x/admin/types"
)

// GenesisState is the app genesis: module name to raw JSON.
type GenesisState map[string]json.RawMessage

// TestDenom funds test accounts at genesis. It is not KUSD: KUSD supply starts at zero
// (design §10.4), and fees are zero, so balances only matter for tests and demos.
const TestDenom = "utest"

// BondDenom is the native staking token (design §7.7, M7; name is a default
// assumption — review).
const BondDenom = "ukey"

// Staking defaults (design §10.3, M7).
const (
	DefaultMaxValidators = 50
	DefaultUnbondingTime = 21 * 24 * time.Hour
)

// DefaultSelfBond is a genesis validator's self-bond when none is given: 1e12 ukey,
// consensus power 1,000,000 with the SDK's PowerReduction of 1e6.
var DefaultSelfBond = sdkmath.NewInt(1_000_000_000_000)

// GenesisOperator is one initial validator: bonded with SelfBond (DefaultSelfBond if
// nil or zero) and allowlisted.
type GenesisOperator struct {
	Address    sdk.AccAddress
	ConsPubKey []byte // raw 32-byte ed25519
	Moniker    string
	SelfBond   sdkmath.Int
}

// GenesisInput describes a Keystone genesis app state.
type GenesisInput struct {
	Admin     sdk.AccAddress
	Operators []GenesisOperator
	// Accounts get a BaseAccount and, if FundAmount is positive, a TestDenom and a
	// BondDenom balance of FundAmount each. Operators always get an account.
	Accounts   []sdk.AccAddress
	FundAmount sdkmath.Int
}

// PatchDefaults sets the Keystone staking and mint params in a default genesis (bond
// denom, max validators, unbonding time, mint denom).
func PatchDefaults(cdc codec.JSONCodec, gs GenesisState) GenesisState {
	var st stakingtypes.GenesisState
	cdc.MustUnmarshalJSON(gs[stakingtypes.ModuleName], &st)
	st.Params.BondDenom = BondDenom
	st.Params.MaxValidators = DefaultMaxValidators
	st.Params.UnbondingTime = DefaultUnbondingTime
	// Rotation is rejected by the ante handler (ADR-015); the fee only needs the denom.
	st.Params.KeyRotationFee.Denom = BondDenom
	gs[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&st)

	var mt minttypes.GenesisState
	cdc.MustUnmarshalJSON(gs[minttypes.ModuleName], &mt)
	mt.Params.MintDenom = BondDenom
	gs[minttypes.ModuleName] = cdc.MustMarshalJSON(&mt)
	return gs
}

// BuildGenesis returns the default genesis with x/admin, x/staking, auth and bank
// filled from in (design §10.4). Each operator becomes a bonded validator with a
// self-delegation of its SelfBond; x/staking's InitGenesis bonds it and
// returns the initial validator set. Operators are sorted by address; duplicate
// accounts are skipped.
func BuildGenesis(cdc codec.JSONCodec, defaults GenesisState, in GenesisInput) (GenesisState, error) {
	gs := make(GenesisState, len(defaults))
	for k, v := range defaults {
		gs[k] = v
	}

	ops := append([]GenesisOperator(nil), in.Operators...)
	sortOperators(ops)

	adminGen := admintypes.GenesisState{Params: admintypes.Params{Admin: in.Admin.String()}}
	for _, op := range ops {
		adminGen.Allowlist = append(adminGen.Allowlist, admintypes.AllowEntry{Operator: op.Address.String(), Moniker: op.Moniker})
	}
	if err := adminGen.Validate(); err != nil {
		return nil, fmt.Errorf("x/admin genesis: %w", err)
	}
	gs[admintypes.ModuleName] = cdc.MustMarshalJSON(&adminGen)

	var stakingGen stakingtypes.GenesisState
	cdc.MustUnmarshalJSON(gs[stakingtypes.ModuleName], &stakingGen)
	bonded := sdkmath.ZeroInt()
	for _, op := range ops {
		if len(op.ConsPubKey) != ed25519.PubKeySize {
			return nil, fmt.Errorf("operator %s: consensus key must be %d bytes", op.Address, ed25519.PubKeySize)
		}
		bond := op.SelfBond
		if bond.IsNil() || bond.IsZero() {
			bond = DefaultSelfBond
		}
		if !bond.IsPositive() {
			return nil, fmt.Errorf("operator %s: self-bond must be positive", op.Address)
		}
		valoper := sdk.ValAddress(op.Address).String()
		v, err := stakingtypes.NewValidator(valoper, &ed25519.PubKey{Key: slices.Clone(op.ConsPubKey)}, stakingtypes.Description{Moniker: op.Moniker})
		if err != nil {
			return nil, err
		}
		v.Tokens = bond
		v.DelegatorShares = sdkmath.LegacyNewDecFromInt(bond)
		v.MinSelfDelegation = sdkmath.OneInt()
		v.Commission = stakingtypes.NewCommission(sdkmath.LegacyNewDecWithPrec(5, 2), sdkmath.LegacyNewDecWithPrec(20, 2), sdkmath.LegacyNewDecWithPrec(1, 2))
		stakingGen.Validators = append(stakingGen.Validators, v)
		stakingGen.Delegations = append(stakingGen.Delegations,
			stakingtypes.NewDelegation(op.Address.String(), valoper, sdkmath.LegacyNewDecFromInt(bond)))
		bonded = bonded.Add(bond)
	}
	gs[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stakingGen)
	bondDenom := stakingGen.Params.BondDenom

	var accounts authtypes.GenesisAccounts
	var balances []banktypes.Balance
	supply := sdk.NewCoins()
	seen := map[string]bool{}
	all := append(append([]sdk.AccAddress(nil), in.Accounts...), operatorAddrs(ops)...)
	for _, addr := range all {
		if seen[addr.String()] {
			continue
		}
		seen[addr.String()] = true
		accounts = append(accounts, authtypes.NewBaseAccount(addr, nil, uint64(len(accounts)), 0))
		if !in.FundAmount.IsNil() && in.FundAmount.IsPositive() {
			coins := sdk.NewCoins(sdk.NewCoin(TestDenom, in.FundAmount), sdk.NewCoin(bondDenom, in.FundAmount))
			balances = append(balances, banktypes.Balance{Address: addr.String(), Coins: coins})
			supply = supply.Add(coins...)
		}
	}
	if bonded.IsPositive() {
		// Unbonded at genesis: staking InitGenesis bonds them (moving the tokens to the
		// bonded pool), which fires the hooks that create slashing signing infos.
		pool := sdk.NewCoins(sdk.NewCoin(bondDenom, bonded))
		balances = append(balances, banktypes.Balance{Address: authtypes.NewModuleAddress(stakingtypes.NotBondedPoolName).String(), Coins: pool})
		supply = supply.Add(pool...)
	}
	var authGen authtypes.GenesisState
	cdc.MustUnmarshalJSON(gs[authtypes.ModuleName], &authGen)
	packed, err := authtypes.PackAccounts(accounts)
	if err != nil {
		return nil, err
	}
	authGen.Accounts = packed
	gs[authtypes.ModuleName] = cdc.MustMarshalJSON(&authGen)

	var bankGen banktypes.GenesisState
	cdc.MustUnmarshalJSON(gs[banktypes.ModuleName], &bankGen)
	bankGen.Balances = banktypes.SanitizeGenesisBalances(balances)
	bankGen.Supply = supply
	gs[banktypes.ModuleName] = cdc.MustMarshalJSON(&bankGen)
	return gs, nil
}

func operatorAddrs(ops []GenesisOperator) []sdk.AccAddress {
	out := make([]sdk.AccAddress, len(ops))
	for i, op := range ops {
		out[i] = op.Address
	}
	return out
}

func sortOperators(ops []GenesisOperator) {
	slices.SortFunc(ops, func(a, b GenesisOperator) int { return bytes.Compare(a.Address, b.Address) })
}
