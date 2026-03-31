package types

// DefaultGenesis holds the default params.
func DefaultGenesis() *GenesisState { return &GenesisState{Params: DefaultParams()} }

// Validate checks the params.
func (gs GenesisState) Validate() error { return gs.Params.Validate() }
