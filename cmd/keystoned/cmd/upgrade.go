package cmd

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

// adminUpgradeCmd is `keystoned tx admin-upgrade [name] [height]`: MsgSoftwareUpgrade
// with the x/admin admin as authority (design §17; there is no x/gov). The admin is the
// operators' multisig, so it is used with --generate-only, then tx sign / multi-sign.
func adminUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin-upgrade [name] [height]",
		Short: "Schedule an x/upgrade plan, signed by the x/admin admin (--from admin --generate-only)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cctx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			height, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return err
			}
			info, _ := cmd.Flags().GetString("upgrade-info")
			msg := &upgradetypes.MsgSoftwareUpgrade{
				Authority: cctx.GetFromAddress().String(),
				Plan:      upgradetypes.Plan{Name: args[0], Height: height, Info: info},
			}
			return tx.GenerateOrBroadcastTxCLI(cctx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String("upgrade-info", "", "plan info (e.g. binary download links)")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
