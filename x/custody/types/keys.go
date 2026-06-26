package types

import "cosmossdk.io/collections"

const (
	ModuleName = "custody"
	StoreKey   = ModuleName
)

// Store prefixes (design §10.1). Never reuse a value.
var (
	ParamsKey          = collections.NewPrefix(0x01)
	EpochsKey          = collections.NewPrefix(0x02) // epoch -> KeyEpoch
	ActiveEpochKey     = collections.NewPrefix(0x03) // active epoch number
	ScriptVaultKey     = collections.NewPrefix(0x04) // pk_script -> vault id
	VaultScriptKey     = collections.NewPrefix(0x05) // (vault id, epoch) -> pk_script
	TalliesKey         = collections.NewPrefix(0x06) // attestation hash -> Tally
	AnchorKey          = collections.NewPrefix(0x07) // BtcBlockRef
	UtxosKey           = collections.NewPrefix(0x08) // (txid, vout) -> Utxo
	UtxoIndexKey       = collections.NewPrefix(0x09) // (status, inverted amount, txid‖vout) -> empty
	ShareIndexKey      = collections.NewPrefix(0x11) // (epoch, operator) -> share index
	VerifyingSharesKey = collections.NewPrefix(0x12) // (epoch, share) -> Y_i
	ClaimableKey       = collections.NewPrefix(0x15) // address -> claimable sat
	FeeReserveKey      = collections.NewPrefix(0x16) // fee reserve sat
	DeficitKey         = collections.NewPrefix(0x17) // custody deficit sat
	CreditedKey        = collections.NewPrefix(0x1A) // credited sat (for the cap)
	DepositsKey        = collections.NewPrefix(0x1C) // (txid, vout) -> DepositRecord (added in M3)
	TallyExpiryKey     = collections.NewPrefix(0x1D) // (first height, hash) -> empty (added in M3)
)
