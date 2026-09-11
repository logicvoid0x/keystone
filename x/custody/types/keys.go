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
	RequestsKey        = collections.NewPrefix(0x0A) // request id -> WithdrawalRequest (M4a)
	NextRequestKey     = collections.NewPrefix(0x0B) // next request id
	BatchesKey         = collections.NewPrefix(0x0C) // batch id -> WithdrawalBatch
	SessionsKey        = collections.NewPrefix(0x0D) // session id -> SigningSession
	ActiveSessionsKey  = collections.NewPrefix(0x0E) // session id -> empty (OPEN or STALLED)
	CommitmentsKey     = collections.NewPrefix(0x0F) // (epoch, share, seq) -> StoredCommitment (unconsumed)
	PoolHeadKey        = collections.NewPrefix(0x10) // (epoch, share) -> last recorded seq
	ShareIndexKey      = collections.NewPrefix(0x11) // (epoch, operator) -> share index
	VerifyingSharesKey = collections.NewPrefix(0x12) // (epoch, share) -> Y_i
	ClaimableKey       = collections.NewPrefix(0x15) // address -> claimable sat
	FeeReserveKey      = collections.NewPrefix(0x16) // fee reserve sat
	DeficitKey         = collections.NewPrefix(0x17) // custody deficit sat
	DKGStatesKey       = collections.NewPrefix(0x13) // epoch -> DKGState (M4b)
	DKGMessagesKey     = collections.NewPrefix(0x14) // (epoch, phase, key) -> message bytes (M4b)
	CustodyKeysKey     = collections.NewPrefix(0x18) // operator -> CustodyKey (M4b)
	SignerStatsKey     = collections.NewPrefix(0x19) // (epoch, operator) -> SignerStats (M4a)
	CreditedKey        = collections.NewPrefix(0x1A) // credited sat (for the cap)
	PendingKey         = collections.NewPrefix(0x1B) // PendingMembership (M5)
	DepositsKey        = collections.NewPrefix(0x1C) // (txid, vout) -> DepositRecord (added in M3)
	TallyExpiryKey     = collections.NewPrefix(0x1D) // (first height, hash) -> empty (added in M3)
	QueueKey           = collections.NewPrefix(0x1E) // queued request id -> empty (added in M4a)
	TxBatchKey         = collections.NewPrefix(0x1F) // txid -> batch id, every version (added in M4a)
	NextBatchKey       = collections.NewPrefix(0x20) // next batch id (added in M4a)
	InFlightKey        = collections.NewPrefix(0x21) // epoch -> batch id not yet confirmed (added in M4a)
	HotKeysKey         = collections.NewPrefix(0x22) // hot key -> operator (added in M4b)
)
