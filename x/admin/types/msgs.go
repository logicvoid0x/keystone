package types

import (
	"bytes"

	errorsmod "cosmossdk.io/errors"

	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

var (
	_ cdctypes.UnpackInterfacesMessage = MsgExec{}

	_ sdk.Msg              = (*MsgUpdateParams)(nil)
	_ sdk.HasValidateBasic = (*MsgUpdateParams)(nil)
	_ sdk.Msg              = (*MsgSetAllowlist)(nil)
	_ sdk.HasValidateBasic = (*MsgSetAllowlist)(nil)
	_ sdk.Msg              = (*MsgExec)(nil)
	_ sdk.HasValidateBasic = (*MsgExec)(nil)
)

// ValidateBasic checks the admin and the new params.
func (m *MsgUpdateParams) ValidateBasic() error {
	if err := validateAddr("admin", m.Admin); err != nil {
		return err
	}
	return m.Params.Validate()
}

// ValidateBasic checks the admin, that something changes, that add and remove are
// each sorted by address bytes without duplicates, and that they do not overlap.
func (m *MsgSetAllowlist) ValidateBasic() error {
	if err := validateAddr("admin", m.Admin); err != nil {
		return err
	}
	if len(m.Add) == 0 && len(m.Remove) == 0 {
		return ErrEmptyChange
	}
	add := make([]string, len(m.Add))
	for i, in := range m.Add {
		add[i] = in.Operator
	}
	addAccs, err := ParseSorted(add)
	if err != nil {
		return err
	}
	rmAccs, err := ParseSorted(m.Remove)
	if err != nil {
		return err
	}
	for _, a := range addAccs {
		for _, r := range rmAccs {
			if bytes.Equal(a, r) {
				return errorsmod.Wrapf(ErrOverlap, "%s", a)
			}
		}
	}
	return nil
}

// ValidateBasic checks the admin and that there is at least one message. The message
// types and their authority are checked by the keeper.
func (m *MsgExec) ValidateBasic() error {
	if err := validateAddr("admin", m.Admin); err != nil {
		return err
	}
	if len(m.Msgs) == 0 {
		return errorsmod.Wrap(ErrExecMsg, "no messages")
	}
	return nil
}

// UnpackInterfaces caches the inner messages when the tx is decoded.
func (m MsgExec) UnpackInterfaces(unpacker cdctypes.AnyUnpacker) error {
	return sdktx.UnpackInterfaces(unpacker, m.Msgs)
}

// SdkMsgs returns the unpacked inner messages.
func (m *MsgExec) SdkMsgs() ([]sdk.Msg, error) { return sdktx.GetMsgs(m.Msgs, "MsgExec") }

// ParseSorted parses bech32 account addresses that must be strictly ascending by bytes.
func ParseSorted(addrs []string) ([]sdk.AccAddress, error) {
	out := make([]sdk.AccAddress, len(addrs))
	for i, s := range addrs {
		acc, err := sdk.AccAddressFromBech32(s)
		if err != nil {
			return nil, errorsmod.Wrapf(ErrInvalidAddress, "%q: %v", s, err)
		}
		out[i] = acc
	}
	return out, CheckSorted(out)
}

// CheckSorted requires addresses to be strictly ascending by bytes.
func CheckSorted(addrs []sdk.AccAddress) error {
	for i := 1; i < len(addrs); i++ {
		if bytes.Compare(addrs[i-1], addrs[i]) >= 0 {
			return errorsmod.Wrapf(ErrUnsorted, "at index %d", i)
		}
	}
	return nil
}

func validateAddr(field, addr string) error {
	if _, err := sdk.AccAddressFromBech32(addr); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "%s %q: %v", field, addr, err)
	}
	return nil
}
