package cmd

import (
	"klog/internal/engine"

	"github.com/spf13/cobra"
)

var traceCmd = &cobra.Command{
	Use:   "trace [resource]",
	Short: "klog v5 - Trace ID 追踪",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := engine.New(ctx, logCfg)
		if err != nil {
			return err
		}

		// 挂载日志模组实现基础追踪
		logMod := engine.NewLogsModule(args[0])
		_ = eng.AttachModule(logMod)

		if err := eng.Start(); err != nil {
			return err
		}
		<-ctx.Done()
		eng.Stop()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(traceCmd)
}
