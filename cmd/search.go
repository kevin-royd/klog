package cmd

import (
	"fmt"
	icolor "klog/internal/color"
	"klog/internal/k8s"

	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search [keyword]",
	Short: "跨命名空间搜索资源",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		adapter, err := k8s.NewAdapter(logCfg.Kubeconfig, logCfg.Namespace)
		if err != nil {
			return err
		}

		icolor.Info("正在集群中搜索: %s", args[0])
		// 逻辑由适配器承载
		pods, _ := adapter.ResolvePods(ctx, args[0])
		for _, p := range pods {
			fmt.Printf("found: %s/%s\n", p.Namespace, p.Name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
}
