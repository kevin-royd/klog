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
	Short: "高性能 Kubernetes 日志系统",
	Long:  `klog Engine 驱动：支持资源秒级解析、滚动更新自动追踪、全链路生命周期管理。`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. 从命令中提取 Root Context (由 main 注入)
		ctx := cmd.Context()

		// 2. 初始化核心引擎 (Engine 驱动系统)
		eng, err := engine.New(ctx, logCfg)
		if err != nil {
			return fmt.Errorf("引擎初始化失败: %w", err)
		}

		// 3. 将任务交给引擎处理
		return eng.ExecuteLogs(args[0])
	},
}

func init() {
	// 绑定 Flags
	logCfg.BindFlags(logsCmd)
	rootCmd.AddCommand(logsCmd)
}
