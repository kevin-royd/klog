package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"klog/internal/config"
	"klog/internal/engine"
	"klog/kubernetes"

	"github.com/spf13/cobra"
)

var (
	cfg = config.NewDefaultConfig()
)

var logsCmd = &cobra.Command{
	Use:   "logs [资源类型/名称]",
	Short: "查看资源日志",
	Long:  `klog 核心功能：自动解析资源，动态追踪 Pod 重建过程中的日志。`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. 获取全局生命周期 Context
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		// 2. 初始化底层 Adapter (依赖注入准备)
		client, err := kubernetes.NewClient(cfg.Kubeconfig, cfg.Namespace)
		if err != nil {
			return err
		}

		// 3. 启动 Service 层
		eng := engine.NewEngine(ctx, cfg, client)

		// 4. 执行业务逻辑
		return eng.RunLogs(args[0])
	},
}

func init() {
	// 将 Flags 绑定到 cfg 结构体，杜绝全局状态变量
	cfg.BindFlags(logsCmd)
	rootCmd.AddCommand(logsCmd)
}
