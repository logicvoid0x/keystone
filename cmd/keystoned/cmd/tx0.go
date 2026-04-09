package cmd

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/OWNER/keystone/app/protocol"
)

// decodeTx0Cmd decodes a tx0 (base64, as CometBFT RPC returns txs) and prints its
// extended commit as JSON. Exit status is non-zero when the bytes are not a tx0.
func decodeTx0Cmd() *cobra.Command {
	return &cobra.Command{
		Use:   "decode-tx0 [base64-tx]",
		Short: "Decode an injected-votes tx0 (KSTN | 0x01 | InjectedVotes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bz, err := base64.StdEncoding.DecodeString(args[0])
			if err != nil {
				return fmt.Errorf("not base64: %w", err)
			}
			ext, ok := protocol.DecodeInjected(bz)
			if !ok {
				return fmt.Errorf("not a tx0")
			}
			type vote struct {
				Address     string `json:"address"`
				Power       int64  `json:"power"`
				BlockIDFlag string `json:"block_id_flag"`
				VEBytes     int    `json:"ve_bytes"`
				Signed      bool   `json:"signed"`
			}
			out := struct {
				Round int32  `json:"round"`
				Votes []vote `json:"votes"`
			}{Round: ext.Round}
			for _, v := range ext.Votes {
				out.Votes = append(out.Votes, vote{
					Address:     hex.EncodeToString(v.Validator.Address),
					Power:       v.Validator.Power,
					BlockIDFlag: v.BlockIdFlag.String(),
					VEBytes:     len(v.VoteExtension),
					Signed:      len(v.ExtensionSignature) > 0,
				})
			}
			enc, err := json.Marshal(out)
			if err != nil {
				return err
			}
			cmd.Println(string(enc))
			return nil
		},
	}
}
