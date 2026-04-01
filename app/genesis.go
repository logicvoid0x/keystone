package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	poatypes "github.com/OWNER/keystone/x/poa/types"
)

// GenesisState is the app genesis: module name to raw JSON.
type GenesisState map[string]json.RawMessage

// TestDenom funds test accounts at genesis. It is not KUSD: KUSD supply starts at zero
// (design §10.4), and fees are zero, so balances only matter for tests and demos.
const TestDenom = "utest"

// GenesisOperator is one initial x/poa operator (power 1).
type GenesisOperator struct {
	Address    sdk.AccAddress
	ConsPubKey []byte // raw 32-byte ed25519
	Moniker    string
}

// GenesisInput describes a Keystone genesis app state.
type GenesisInput struct {
	Admin     sdk.AccAddress
	Operators []GenesisOperator
	// Accounts get a BaseAccount and, if FundAmount is positive, a TestDenom balance.
	Accounts   []sdk.AccAddress
	FundAmount sdkmath.Int
}

// BuildGenesis returns the default genesis with x/poa, auth and bank filled from in.
// Operators are sorted by address; duplicate accounts are skipped.
func BuildGenesis(cdc codec.JSONCodec, defaults GenesisState, in GenesisInput) (GenesisState, error) {
	gs := make(GenesisState, len(defaults))
	for k, v := range defaults {
		gs[k] = v
	}

	poaGen := poatypes.GenesisState{Params: poatypes.Params{Admin: in.Admin.String()}}
	ops := append([]GenesisOperator(nil), in.Operators...)
	sortOperators(ops)
	for _, op := range ops {
		poaGen.Operators = append(poaGen.Operators, poatypes.Operator{
			OperatorAddress: op.Address.String(),
			ConsensusPubkey: op.ConsPubKey,
			Moniker:         op.Moniker,
			Power:           1,
		})
	}
	if err := poaGen.Validate(); err != nil {
		return nil, fmt.Errorf("x/poa genesis: %w", err)
	}
	gs[poatypes.ModuleName] = cdc.MustMarshalJSON(&poaGen)

	var accounts authtypes.GenesisAccounts
	var balances []banktypes.Balance
	supply := sdk.NewCoins()
	seen := map[string]bool{}
	for i, addr := range in.Accounts {
		if seen[addr.String()] {
			continue
		}
		seen[addr.String()] = true
		accounts = append(accounts, authtypes.NewBaseAccount(addr, nil, uint64(i), 0))
		if !in.FundAmount.IsNil() && in.FundAmount.IsPositive() {
			coins := sdk.NewCoins(sdk.NewCoin(TestDenom, in.FundAmount))
			balances = append(balances, banktypes.Balance{Address: addr.String(), Coins: coins})
			supply = supply.Add(coins...)
		}
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

func sortOperators(ops []GenesisOperator) {
	slices.SortFunc(ops, func(a, b GenesisOperator) int { return bytes.Compare(a.Address, b.Address) })
}
