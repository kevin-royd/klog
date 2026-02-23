package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	icolor "klog/internal/color"
	"klog/kubernetes"

	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search [关键词]",
	Short: "跨 Namespace 搜索 Pod",
	Long: `跨所有 Namespace 搜索匹配指定关键词的 Pod。

支持按 Pod 名称和标签进行模糊匹配。

示例：
  klog search nginx                   # 搜索包含 "nginx" 的 Pod
  klog search myapp -A                # 跨所有 namespace 搜索
  klog search myapp -n production     # 在指定 namespace 搜索
`,
	Args: cobra.ExactArgs(1),
	RunE: runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
}

func runSearch(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	keyword := args[0]

	client, err := kubernetes.NewClient(kubeconfig, namespace)
	if err != nil {
		return fmt.Errorf("初始化K8s客户端失败: %w", err)
	}

	namespaces, err := resolveNamespaces(ctx, client)
	if err != nil {
		return err
	}

	icolor.Header("搜索 Pod: '%s' (跨 %d 个 Namespace)", keyword, len(namespaces))
	fmt.Println()

	pods, err := client.SearchPods(ctx, keyword, namespaces)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		icolor.Warn("未找到匹配的 Pod")
		return nil
	}

	// 按namespace分组显示
	grouped := make(map[string][]kubernetes.PodInfo)
	for _, pod := range pods {
		grouped[pod.Namespace] = append(grouped[pod.Namespace], pod)
	}

	totalCount := 0
	for ns, nsPods := range grouped {
		icolor.Info("📦 Namespace: %s (%d个Pod)", ns, len(nsPods))
		for _, pod := range nsPods {
			statusIcon := "●"
			switch pod.Status {
			case "Running":
				statusIcon = "🟢"
			case "Pending":
				statusIcon = "🟡"
			case "Failed":
				statusIcon = "🔴"
			case "Succeeded":
				statusIcon = "✅"
			}

			crashInfo := ""
			if pod.CrashLoopBackOff {
				crashInfo = fmt.Sprintf(" ⚠️ CrashLoopBackOff (重启%d次)", pod.RestartCount)
			}

			fmt.Printf("  %s %-50s [%s] node=%s%s\n",
				statusIcon,
				pod.Name,
				strings.Join(pod.Containers, ","),
				pod.NodeName,
				crashInfo,
			)
			totalCount++
		}
		fmt.Println()
	}

	icolor.Separator()
	icolor.Success("共找到 %d 个匹配的 Pod", totalCount)

	return nil
}
