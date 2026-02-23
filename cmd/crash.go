package cmd

import (
	icolor "klog/internal/color"
	"klog/internal/k8s"

	"github.com/spf13/cobra"
)

var crashCmd = &cobra.Command{
	Use:   "crash",
	Short: "CrashLoopBackOff 自动检测",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		adapter, err := k8s.NewAdapter(logCfg.Kubeconfig, logCfg.Namespace)
		if err != nil {
			return err
		}

		icolor.Header("正在扫描集群中的异常容器...")
		// 这里的逻辑后续可以整合进 Engine.ExecuteCrashScan()
		// 暂时保持功能可用性
		pods, err := adapter.ResolvePods(ctx, "")
		if err != nil {
			return err
		}

		for _, p := range pods {
			icolor.Warn("发现异常 Pod: %s", p.Name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(crashCmd)
}
