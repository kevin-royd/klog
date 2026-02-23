package cmd

import (
	"klog/internal/engine"

	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch [resource]",
	Short: "klog v5 - 资源动态监视",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := engine.New(ctx, logCfg)
		if err != nil {
			return err
		}

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
	rootCmd.AddCommand(watchCmd)
}
