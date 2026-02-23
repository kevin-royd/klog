package cmd

import (
	"klog/internal/engine"

	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch [resource]",
	Short: "动态观察 Pod 变化",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := engine.New(ctx, logCfg)
		if err != nil {
			return err
		}
		return eng.RunLogs(args[0])
	},
}

func init() {
	rootCmd.AddCommand(watchCmd)
}
