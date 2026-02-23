package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	icolor "klog/internal/color"
	"klog/kubernetes"

	"github.com/spf13/cobra"
)

var (
	watchUseInformer bool
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "实时监控 Pod 事件",
	Long: `实时监控指定 Namespace（或所有 Namespace）中的 Pod 变化事件。

支持两种监控模式：
  - Watch API (默认): 轻量级，适合简单监控
  - Informer 模式 (--informer): 高效，带本地缓存，适合大规模监控

示例：
  klog watch                          # 监控当前 namespace
  klog watch -A                       # 监控所有 namespace
  klog watch -l app=nginx             # 按标签过滤
  klog watch --informer               # 使用 Informer 模式
`,
	RunE: runWatch,
}

func init() {
	watchCmd.Flags().BoolVar(&watchUseInformer, "informer", false, "使用 Informer 模式（高效，带缓存）")

	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		icolor.Info("\n正在停止监控...")
		cancel()
	}()

	client, err := kubernetes.NewClient(kubeconfig, namespace)
	if err != nil {
		return fmt.Errorf("初始化K8s客户端失败: %w", err)
	}

	if watchUseInformer {
		return runInformerWatch(ctx, client)
	}

	return runAPIWatch(ctx, client)
}

// runAPIWatch 使用Watch API
func runAPIWatch(ctx context.Context, client *kubernetes.Client) error {
	printer := icolor.NewPrinter()
	watcher := kubernetes.NewPodWatcher(client, printer)
	defer watcher.Stop()

	namespaces, err := resolveNamespaces(ctx, client)
	if err != nil {
		return err
	}

	icolor.Header("Pod Watch (Watch API模式)")
	icolor.Info("监控范围: %d 个 Namespace", len(namespaces))
	if labelSelector != "" {
		icolor.Info("标签选择器: %s", labelSelector)
	}
	icolor.Separator()

	err = watcher.WatchMultiNamespace(ctx, namespaces, labelSelector)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Events():
			watcher.PrintEvent(event)
		case <-ctx.Done():
			return nil
		}
	}
}

// runInformerWatch 使用Informer
func runInformerWatch(ctx context.Context, client *kubernetes.Client) error {
	ns := client.Namespace
	if allNamespaces {
		ns = "" // 空namespace = 所有namespace
	}

	informer := kubernetes.NewPodInformer(client, ns)
	if labelSelector != "" {
		informer.SetLabelSelector(labelSelector)
	}

	icolor.Header("Pod Watch (Informer模式 - 高效)")
	if allNamespaces {
		icolor.Info("监控范围: 所有 Namespace")
	} else {
		icolor.Info("监控范围: %s", ns)
	}
	if labelSelector != "" {
		icolor.Info("标签选择器: %s", labelSelector)
	}
	icolor.Separator()

	if err := informer.Start(ctx); err != nil {
		return fmt.Errorf("启动Informer失败: %w", err)
	}
	defer informer.Stop()

	// 列出当前Pod
	pods, err := informer.ListPods()
	if err == nil && len(pods) > 0 {
		icolor.Info("当前已有 %d 个 Pod:", len(pods))
		for _, pod := range pods {
			fmt.Printf("  ● %s/%s (%s)\n", pod.Namespace, pod.Name, pod.Status.Phase)
		}
		icolor.Separator()
	}

	icolor.Info("开始监控Pod事件 (Ctrl+C退出)...")
	fmt.Println()

	for {
		select {
		case event := <-informer.Events():
			informer.PrintEvent(event)
		case <-ctx.Done():
			return nil
		}
	}
}
