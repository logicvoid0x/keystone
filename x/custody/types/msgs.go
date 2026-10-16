package types

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.Msg              = (*MsgUpdateParams)(nil)
	_ sdk.HasValidateBasic = (*MsgUpdateParams)(nil)
)

// ValidateBasic checks the signer address and the params bounds.
func (m *MsgUpdateParams) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Admin); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "admin %q: %v", m.Admin, err)
	}
	return m.Params.Validate()
}

var (
	_ sdk.Msg              = (*MsgWithdrawClaim)(nil)
	_ sdk.HasValidateBasic = (*MsgWithdrawClaim)(nil)
)

// ValidateBasic checks the owner, the recipient script kind and a positive amount.
func (m *MsgWithdrawClaim) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Owner); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "owner %q: %v", m.Owner, err)
	}
	if _, err := WithdrawScriptKind(m.RecipientScript); err != nil {
		return errorsmod.Wrap(ErrInvalidScript, err.Error())
	}
	if m.AmountSat.IsNil() || !m.AmountSat.IsPositive() {
		return errorsmod.Wrapf(ErrInvalidAmount, "amount %v", m.AmountSat)
	}
	return nil
}

var (
	_ sdk.Msg              = (*MsgRegisterCustodyKey)(nil)
	_ sdk.HasValidateBasic = (*MsgRegisterCustodyKey)(nil)
	_ sdk.Msg              = (*MsgDKGCommit)(nil)
	_ sdk.HasValidateBasic = (*MsgDKGCommit)(nil)
	_ sdk.Msg              = (*MsgDKGDeal)(nil)
	_ sdk.HasValidateBasic = (*MsgDKGDeal)(nil)
	_ sdk.Msg              = (*MsgDKGComplaint)(nil)
	_ sdk.HasValidateBasic = (*MsgDKGComplaint)(nil)
)

func checkAddr(field, s string) error {
	if _, err := sdk.AccAddressFromBech32(s); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "%s %q: %v", field, s, err)
	}
	return nil
}

// ValidateBasic checks the addresses and that the encryption key is a valid
// compressed secp256k1 point.
func (m *MsgRegisterCustodyKey) ValidateBasic() error {
	if err := checkAddr("operator", m.Operator); err != nil {
		return err
	}
	if err := checkAddr("hot_key", m.HotKey); err != nil {
		return err
	}
	return ValidateCustodyKey(CustodyKey{Operator: m.Operator, EncPubkey: m.EncPubkey, HotKey: m.HotKey})
}

// ValidateBasic checks the signer and the encodings (33-byte points, 64-byte proof).
func (m *MsgDKGCommit) ValidateBasic() error {
	if err := checkAddr("hot_key", m.HotKey); err != nil {
		return err
	}
	if len(m.Commitments) == 0 || len(m.Commitments) > MaxDKGParticipants {
		return errorsmod.Wrapf(ErrDKG, "%d commitments", len(m.Commitments))
	}
	for _, c := range m.Commitments {
		if len(c) != 33 {
			return errorsmod.Wrap(ErrDKG, "commitments are 33-byte points")
		}
	}
	if len(m.Proof) != 64 {
		return errorsmod.Wrap(ErrDKG, "proof is 64 bytes")
	}
	return nil
}

// ValidateBasic checks the signer and that ciphertexts are sorted by recipient.
func (m *MsgDKGDeal) ValidateBasic() error {
	if err := checkAddr("hot_key", m.HotKey); err != nil {
		return err
	}
	if len(m.Ciphertexts) > MaxDKGParticipants {
		return errorsmod.Wrapf(ErrDKG, "%d ciphertexts", len(m.Ciphertexts))
	}
	for i := 1; i < len(m.Ciphertexts); i++ {
		if m.Ciphertexts[i-1].Recipient >= m.Ciphertexts[i].Recipient {
			return errorsmod.Wrap(ErrDKG, "ciphertexts not sorted by recipient")
		}
	}
	return nil
}

// ValidateBasic checks the addresses and the encodings.
func (m *MsgDKGComplaint) ValidateBasic() error {
	if err := checkAddr("hot_key", m.HotKey); err != nil {
		return err
	}
	if err := checkAddr("dealer", m.Dealer); err != nil {
		return err
	}
	if len(m.EcdhSecret) != 33 || len(m.DleqProof) != 64 {
		return errorsmod.Wrap(ErrDKG, "ecdh secret is 33 bytes, proof 64")
	}
	return nil
}

var (
	_ sdk.Msg              = (*MsgClearTheftPause)(nil)
	_ sdk.HasValidateBasic = (*MsgClearTheftPause)(nil)
)

// ValidateBasic checks the signer address.
func (m *MsgClearTheftPause) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Admin); err != nil {
		return errorsmod.Wrapf(ErrInvalidAddress, "admin %q: %v", m.Admin, err)
	}
	return nil
}
