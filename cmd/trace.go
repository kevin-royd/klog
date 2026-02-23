package cmd

import (
	"klog/internal/engine"

	"github.com/spf13/cobra"
)

var traceCmd = &cobra.Command{
	Use:   "trace [resource]",
	Short: "Trace ID 跨 Pod 关联",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := engine.New(ctx, logCfg)
		if err != nil {
			return err
		}
		return eng.ExecuteLogs(args[0]) // 对于简单的日志流，共用一个逻辑出口
	},
}

func init() {
	rootCmd.AddCommand(traceCmd)
}
