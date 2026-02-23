package cmd

import (
	"fmt"
	"klog/internal/config"
	"klog/internal/engine"

	"github.com/spf13/cobra"
)

var (
	logCfg = config.NewDefaultConfig()
)

var logsCmd = &cobra.Command{
	Use:   "logs [资源类型/名称]",
	Short: "klog v5 - 高性能模组化日志引擎",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		// 1. 初始化引擎底座
		eng, err := engine.New(ctx, logCfg)
		if err != nil {
			return fmt.Errorf("引擎初始化失败: %w", err)
		}

		// 2. 挂载日志模组 (模块化设计)
		logMod := engine.NewLogsModule(args[0])
		if err := eng.AttachModule(logMod); err != nil {
			return err
		}

		// 3. 启动引擎并阻塞直到退出
		if err := eng.Start(); err != nil {
			return err
		}

		<-ctx.Done()
		eng.Stop()
		return nil
	},
}

func init() {
	logCfg.BindFlags(logsCmd)
	rootCmd.AddCommand(logsCmd)
}
