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

	"klog/internal/cache"
	icolor "klog/internal/color"
	itrace "klog/internal/trace"
	"klog/kubernetes"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

var (
	traceID string
)

var traceCmd = &cobra.Command{
	Use:   "trace [资源类型/名称]",
	Short: "Trace ID 自动聚合",
	Long: `从指定资源的日志中自动提取 Trace ID，并聚合展示关联的所有日志。

支持的 Trace ID 格式：
  - OpenTelemetry/Jaeger (32位hex)
  - W3C Trace Context
  - UUID 格式
  - 自定义 trace_id 字段

示例：
  klog trace deploy/myapp                     # 聚合所有trace
  klog trace deploy/myapp --trace-id abc123   # 查看指定trace
  klog trace deploy/myapp -A                  # 跨namespace聚合
  klog trace deploy/myapp --since 1h          # 最近1小时
`,
	Args: cobra.ExactArgs(1),
	RunE: runTrace,
}

func init() {
	traceCmd.Flags().StringVar(&traceID, "trace-id", "", "指定 Trace ID")
	traceCmd.Flags().StringVar(&since, "since", "1h", "起始时间")

	rootCmd.AddCommand(traceCmd)
}

func runTrace(cmd *cobra.Command, args []string) error {
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

	printer := icolor.NewPrinter()
	resolver := kubernetes.NewResolver(client)
	ref := resolver.ParseResource(args[0])
	traceAgg := itrace.NewAggregator(printer)

	namespaces, err := resolveNamespaces(ctx, client)
	if err != nil {
		return err
	}

	icolor.Header("Trace 聚合分析")

	// 解析所有Pod
	var allPods []kubernetes.PodInfo
	for _, ns := range namespaces {
		pods, err := resolver.ResolveToPods(ctx, ref, ns)
		if err != nil {
			if !allNamespaces {
				return err
			}
			continue
		}
		allPods = append(allPods, pods...)
	}

	if len(allPods) == 0 {
		return fmt.Errorf("未找到匹配的Pod")
	}

	icolor.Info("扫描 %d 个 Pod 的日志...", len(allPods))

	// 构建日志选项
	logOpts := &corev1.PodLogOptions{
		Timestamps: true,
	}
	if since != "" {
		d, err := time.ParseDuration(since)
		if err == nil {
			seconds := int64(d.Seconds())
			logOpts.SinceSeconds = &seconds
		}
	}

	// 并发读取日志并聚合trace
	var wg sync.WaitGroup
	for _, pod := range allPods {
		for _, cont := range pod.Containers {
			wg.Add(1)
			go func(pod kubernetes.PodInfo, containerName string) {
				defer wg.Done()
				scanPodForTraces(ctx, client, traceAgg, pod, containerName, logOpts)
			}(pod, cont)
		}
	}
	wg.Wait()

	fmt.Println()

	// 输出结果
	if traceID != "" {
		traceAgg.PrintTraceDetail(traceID)
	} else {
		traceAgg.PrintTraceSummary()
	}

	return nil
}

// scanPodForTraces 扫描Pod日志中的trace
func scanPodForTraces(ctx context.Context, client *kubernetes.Client,
	traceAgg *itrace.Aggregator, pod kubernetes.PodInfo,
	containerName string, opts *corev1.PodLogOptions) {

	logOpts := opts.DeepCopy()
	logOpts.Container = containerName

	req := client.Clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logOpts)
	readCloser, err := req.Stream(ctx)
	if err != nil {
		return
	}
	defer readCloser.Close()

	scanner := bufio.NewScanner(readCloser)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	lineNum := 0

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		lineNum++

		entry := &cache.LogEntry{
			Namespace:     pod.Namespace,
			PodName:       pod.Name,
			ContainerName: containerName,
			Line:          line,
			LineNumber:    lineNum,
			Level:         detectLogLevel(line),
		}

		// 尝试解析时间戳
		parts := strings.SplitN(line, " ", 2)
		if len(parts) >= 2 {
			t, err := time.Parse(time.RFC3339Nano, parts[0])
			if err == nil {
				entry.Timestamp = t
			}
		}

		traceAgg.AddLogEntry(entry)
	}
}
