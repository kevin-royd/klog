package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	icolor "klog/internal/color"
	"klog/kubernetes"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	crashAutoLogs  bool
	crashTailLines int64
)

var crashCmd = &cobra.Command{
	Use:   "crash",
	Short: "CrashLoopBackOff 自动捕获",
	Long: `自动发现处于 CrashLoopBackOff 状态的 Pod，并展示相关信息和日志。

功能：
  - 扫描所有 CrashLoopBackOff 状态的 Pod
  - 显示容器状态、重启次数、终止原因
  - 自动获取上一次容器的日志（--auto-logs）
  - 支持跨 Namespace 搜索

示例：
  klog crash                          # 当前namespace
  klog crash -A                       # 所有namespace
  klog crash -A --auto-logs           # 自动获取崩溃日志
  klog crash -A --tail 50             # 显示最后50行日志
`,
	RunE: runCrash,
}

func init() {
	crashCmd.Flags().BoolVar(&crashAutoLogs, "auto-logs", false, "自动获取崩溃Pod的日志")
	crashCmd.Flags().Int64Var(&crashTailLines, "tail", 30, "日志尾部行数")

	rootCmd.AddCommand(crashCmd)
}

func runCrash(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	client, err := kubernetes.NewClient(kubeconfig, namespace)
	if err != nil {
		return fmt.Errorf("初始化K8s客户端失败: %w", err)
	}

	namespaces, err := resolveNamespaces(ctx, client)
	if err != nil {
		return err
	}

	icolor.Header("CrashLoopBackOff 扫描")
	icolor.Info("扫描范围: %d 个 Namespace", len(namespaces))
	icolor.Separator()

	// 扫描所有namespace
	var crashPods []kubernetes.PodInfo
	for _, ns := range namespaces {
		pods, err := client.GetPodsByNamespace(ctx, ns)
		if err != nil {
			continue
		}
		for _, pod := range pods {
			if pod.CrashLoopBackOff {
				crashPods = append(crashPods, pod)
			}
		}
	}

	if len(crashPods) == 0 {
		icolor.Success("✨ 太好了！没有发现 CrashLoopBackOff 的 Pod")
		return nil
	}

	icolor.Error("发现 %d 个 CrashLoopBackOff 的 Pod:", len(crashPods))
	fmt.Println()

	for i, pod := range crashPods {
		fmt.Printf("  🔴 [%d] %s/%s\n", i+1, pod.Namespace, pod.Name)
		fmt.Printf("     状态: %s | 重启次数: %d | 节点: %s\n",
			pod.Status, pod.RestartCount, pod.NodeName)
		fmt.Printf("     容器: %s\n", strings.Join(pod.Containers, ", "))

		// 显示标签
		if len(pod.Labels) > 0 {
			var labelParts []string
			for k, v := range pod.Labels {
				labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
			}
			fmt.Printf("     标签: %s\n", strings.Join(labelParts, ", "))
		}
		fmt.Println()
	}

	// 自动获取崩溃日志
	if crashAutoLogs {
		icolor.Separator()
		icolor.Header("崩溃日志")
		fmt.Println()

		printer := icolor.NewPrinter()
		var wg sync.WaitGroup

		for _, pod := range crashPods {
			for _, cont := range pod.Containers {
				wg.Add(1)
				go func(pod kubernetes.PodInfo, containerName string) {
					defer wg.Done()
					fetchCrashLogs(ctx, client, printer, pod, containerName)
				}(pod, cont)
			}
		}
		wg.Wait()
	}

	return nil
}

// fetchCrashLogs 获取崩溃容器的日志
func fetchCrashLogs(ctx context.Context, client *kubernetes.Client,
	printer *icolor.Printer, pod kubernetes.PodInfo, containerName string) {

	icolor.Info("📋 %s/%s/%s 的前一次运行日志:", pod.Namespace, pod.Name, containerName)

	// 获取前一个容器的日志
	opts := &corev1.PodLogOptions{
		Container: containerName,
		Previous:  true,
		TailLines: &crashTailLines,
	}

	req := client.Clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts)
	readCloser, err := req.Stream(ctx)
	if err != nil {
		// 尝试当前容器日志
		opts.Previous = false
		req = client.Clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts)
		readCloser, err = req.Stream(ctx)
		if err != nil {
			icolor.Warn("  无法获取日志: %v", err)
			return
		}
	}
	defer readCloser.Close()

	scanner := bufio.NewScanner(readCloser)
	hasOutput := false
	for scanner.Scan() {
		printer.PrintLog(pod.Namespace, pod.Name, containerName, scanner.Text())
		hasOutput = true
	}

	if !hasOutput {
		icolor.Warn("  无日志输出")
	}
	icolor.Separator()

	// 获取Pod事件
	fetchPodEvents(ctx, client, pod)
}

// fetchPodEvents 获取Pod相关事件
func fetchPodEvents(ctx context.Context, client *kubernetes.Client, pod kubernetes.PodInfo) {
	events, err := client.Clientset.CoreV1().Events(pod.Namespace).List(ctx,
		metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", pod.Name),
		})
	if err != nil {
		return
	}

	if len(events.Items) == 0 {
		return
	}

	// 只显示最近的事件
	icolor.Info("📌 相关事件 (%s/%s):", pod.Namespace, pod.Name)
	count := 0
	for idx := len(events.Items) - 1; idx >= 0 && count < 10; idx-- {
		event := events.Items[idx]
		timestamp := event.LastTimestamp.Format("15:04:05")
		if event.LastTimestamp.IsZero() {
			timestamp = event.CreationTimestamp.Format("15:04:05")
		}

		typeIcon := "ℹ️"
		if event.Type == "Warning" {
			typeIcon = "⚠️"
		}

		fmt.Printf("  %s [%s] %s: %s\n", typeIcon, timestamp, event.Reason, event.Message)
		count++
	}
	fmt.Println()

	// 忽略未使用变量
	_ = time.Now()
}
