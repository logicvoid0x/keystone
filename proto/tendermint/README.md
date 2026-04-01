Vendored CometBFT protos, copied unchanged from CometBFT v0.40.0 `proto/tendermint`
(module cache). They are imports only. Go code is not generated from them; `go_package`
points at `github.com/cometbft/cometbft/...`, which registers them under the same file
names (e.g. `tendermint/abci/types.proto`). Keystone uses
`tendermint.abci.ExtendedCommitInfo` in `keystone/ve/v1/ve.proto`.
