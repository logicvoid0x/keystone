// Package fakebank is an in-memory bank keeper for keeper unit tests. It tracks
// balances per address and supply per denom and enforces module Minter/Burner
// permissions given at construction. Test support only.
package fakebank

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// Bank is the fake. It is not safe for concurrent use.
type Bank struct {
	balances map[string]map[string]sdkmath.Int // address -> denom -> amount
	supply   map[string]sdkmath.Int
	perms    map[string][]string // module -> permissions
}

// New returns an empty bank with module permissions (as in app maccPerms).
func New(perms map[string][]string) *Bank {
	return &Bank{balances: map[string]map[string]sdkmath.Int{}, supply: map[string]sdkmath.Int{}, perms: perms}
}

func (b *Bank) has(module, perm string) bool {
	for _, p := range b.perms[module] {
		if p == perm {
			return true
		}
	}
	return false
}

func (b *Bank) bal(addr sdk.AccAddress, denom string) sdkmath.Int {
	if v, ok := b.balances[addr.String()][denom]; ok {
		return v
	}
	return sdkmath.ZeroInt()
}

func (b *Bank) set(addr sdk.AccAddress, denom string, v sdkmath.Int) {
	m, ok := b.balances[addr.String()]
	if !ok {
		m = map[string]sdkmath.Int{}
		b.balances[addr.String()] = m
	}
	m[denom] = v
}

func (b *Bank) sub(addr sdk.AccAddress, coins sdk.Coins) error {
	for _, c := range coins {
		have := b.bal(addr, c.Denom)
		if have.LT(c.Amount) {
			return fmt.Errorf("insufficient funds: %s has %s%s, needs %s", addr, have, c.Denom, c.Amount)
		}
	}
	for _, c := range coins {
		b.set(addr, c.Denom, b.bal(addr, c.Denom).Sub(c.Amount))
	}
	return nil
}

func (b *Bank) add(addr sdk.AccAddress, coins sdk.Coins) {
	for _, c := range coins {
		b.set(addr, c.Denom, b.bal(addr, c.Denom).Add(c.Amount))
	}
}

func modAddr(name string) sdk.AccAddress { return authtypes.NewModuleAddress(name) }

func (b *Bank) MintCoins(_ context.Context, module string, amt sdk.Coins) error {
	if !b.has(module, authtypes.Minter) {
		return fmt.Errorf("module %s cannot mint", module)
	}
	b.add(modAddr(module), amt)
	for _, c := range amt {
		b.supply[c.Denom] = b.Supply(c.Denom).Add(c.Amount)
	}
	return nil
}

func (b *Bank) BurnCoins(_ context.Context, module string, amt sdk.Coins) error {
	if !b.has(module, authtypes.Burner) {
		return fmt.Errorf("module %s cannot burn", module)
	}
	if err := b.sub(modAddr(module), amt); err != nil {
		return err
	}
	for _, c := range amt {
		b.supply[c.Denom] = b.Supply(c.Denom).Sub(c.Amount)
	}
	return nil
}

func (b *Bank) SendCoinsFromModuleToAccount(_ context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	if err := b.sub(modAddr(module), amt); err != nil {
		return err
	}
	b.add(to, amt)
	return nil
}

func (b *Bank) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	if err := b.sub(from, amt); err != nil {
		return err
	}
	b.add(modAddr(module), amt)
	return nil
}

func (b *Bank) SendCoinsFromModuleToModule(_ context.Context, from, to string, amt sdk.Coins) error {
	if err := b.sub(modAddr(from), amt); err != nil {
		return err
	}
	b.add(modAddr(to), amt)
	return nil
}

func (b *Bank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.bal(addr, denom))
}

func (b *Bank) GetSupply(_ context.Context, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.Supply(denom))
}

// Supply returns the supply of denom.
func (b *Bank) Supply(denom string) sdkmath.Int {
	if v, ok := b.supply[denom]; ok {
		return v
	}
	return sdkmath.ZeroInt()
}

// Balance returns addr's balance of denom.
func (b *Bank) Balance(addr sdk.AccAddress, denom string) sdkmath.Int { return b.bal(addr, denom) }

// ModuleBalance returns a module account's balance of denom.
func (b *Bank) ModuleBalance(module, denom string) sdkmath.Int { return b.bal(modAddr(module), denom) }
