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
	"klog/internal/jsonquery"
	"klog/internal/pipeline"
	"klog/internal/stream"
	itrace "klog/internal/trace"
	"klog/kubernetes"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

var logsCmd = &cobra.Command{
	Use:   "logs [资源类型/名称]",
	Short: "查看资源日志（支持自动资源解析）",
	Long: `查看 Kubernetes 资源的日志。自动解析多种资源类型为 Pod。

支持的资源类型：
  pod/名称, deploy/名称, statefulset/名称, daemonset/名称,
  replicaset/名称, job/名称, cronjob/名称, service/名称

也可以直接输入名称，工具会自动推断资源类型。

示例：
  klog logs deploy/nginx                       # 查看Deployment日志
  klog logs nginx                              # 自动推断资源类型
  klog logs deploy/myapp -f --grep "error"     # 流式查看并过滤
  klog logs deploy/myapp --json ".level,.msg"  # JSON字段选择
  klog logs deploy/myapp --jq "level=error"    # JSON条件过滤
  klog logs deploy/myapp --since 1h --tail 100 # 查看最近1小时，最后100行
  klog logs deploy/myapp -A                    # 跨namespace搜索
  klog logs deploy/myapp --level error,warn    # 按日志级别过滤
  klog logs deploy/myapp --highlight "timeout" # 高亮关键词
`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "流式跟踪日志")
	logsCmd.Flags().StringVar(&since, "since", "", "起始时间 (如: 5s, 2m, 3h, 1d)")
	logsCmd.Flags().Int64Var(&tail, "tail", -1, "显示最后N行")
	logsCmd.Flags().StringVar(&grep, "grep", "", "关键词过滤")
	logsCmd.Flags().BoolVar(&grepInvert, "grep-invert", false, "反转grep匹配")
	logsCmd.Flags().BoolVarP(&grepIgnoreCase, "ignore-case", "i", false, "grep忽略大小写")
	logsCmd.Flags().BoolVar(&previous, "previous", false, "查看上一个容器的日志")
	logsCmd.Flags().BoolVar(&timestamps, "timestamps", false, "显示时间戳")
	logsCmd.Flags().StringVarP(&output, "output", "o", "", "输出格式: raw, flat, table, fields")
	logsCmd.Flags().StringVar(&jsonFields, "json", "", "JSON字段选择 (逗号分隔)")
	logsCmd.Flags().StringArrayVar(&jsonQuery, "jq", nil, "JSON条件过滤 (field=value)")
	logsCmd.Flags().StringArrayVar(&highlight, "highlight", nil, "高亮关键词")
	logsCmd.Flags().StringVar(&level, "level", "", "日志级别过滤 (逗号分隔: error,warn,info)")

	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		icolor.Info("\n正在优雅关闭...")
		cancel()
	}()

	// 初始化K8s客户端
	client, err := kubernetes.NewClient(kubeconfig, namespace)
	if err != nil {
		return fmt.Errorf("初始化K8s客户端失败: %w", err)
	}

	printer := icolor.NewPrinter()
	if len(highlight) > 0 {
		printer.SetKeywords(highlight)
	}

	// 解析资源
	resolver := kubernetes.NewResolver(client)
	ref := resolver.ParseResource(args[0])

	// 确定namespace列表
	namespaces, err := resolveNamespaces(ctx, client)
	if err != nil {
		return err
	}

	// 跨namespace搜索Pod
	var allPods []kubernetes.PodInfo
	for _, ns := range namespaces {
		pods, err := resolver.ResolveToPods(ctx, ref, ns)
		if err != nil {
			if !allNamespaces {
				return err
			}
			continue // 跨namespace时跳过无权限的
		}
		allPods = append(allPods, pods...)
	}

	if len(allPods) == 0 {
		return fmt.Errorf("未找到匹配的Pod")
	}

	icolor.Header("找到 %d 个 Pod", len(allPods))
	for _, pod := range allPods {
		statusIcon := "●"
		switch pod.Status {
		case "Running":
			statusIcon = "🟢"
		case "Pending":
			statusIcon = "🟡"
		case "Failed", "Error":
			statusIcon = "🔴"
		}
		fmt.Printf("  %s %s/%s [%s]\n", statusIcon, pod.Namespace, pod.Name, strings.Join(pod.Containers, ","))
	}
	icolor.Separator()

	// 构建Pipeline
	pipe := buildPipeline()

	// 构建JSON查询器
	jq := buildJSONQuery()

	// 初始化缓存
	logCache := cache.NewLogCache(100, 30*time.Minute)

	// 初始化Trace聚合器
	traceAgg := itrace.NewAggregator(printer)

	// 初始化Stream Pool
	poolSize := len(allPods) * 2
	if poolSize < 10 {
		poolSize = 10
	}
	streamPool := stream.NewStreamPool(client.Clientset, poolSize)
	defer streamPool.CloseAll()

	// 构建PodLogOptions
	logOpts := buildLogOptions()

	// 并发读取日志
	var wg sync.WaitGroup
	for _, pod := range allPods {
		containers := pod.Containers
		if container != "" {
			containers = []string{container}
		}

		for _, cont := range containers {
			wg.Add(1)
			go func(pod kubernetes.PodInfo, containerName string) {
				defer wg.Done()
				streamLogs(ctx, streamPool, printer, pipe, jq, logCache, traceAgg,
					pod, containerName, logOpts)
			}(pod, cont)
		}
	}

	wg.Wait()

	// 打印trace汇总
	traceAgg.PrintTraceSummary()

	// 保存缓存
	logCache.SaveToDisk()

	return nil
}

// streamLogs 流式读取日志
func streamLogs(ctx context.Context, pool *stream.StreamPool, printer *icolor.Printer,
	pipe *pipeline.Pipeline, jq *jsonquery.Query,
	logCache *cache.LogCache, traceAgg *itrace.Aggregator,
	pod kubernetes.PodInfo, containerName string,
	logOpts *corev1.PodLogOptions) {

	opts := logOpts.DeepCopy()
	opts.Container = containerName

	logStream, err := pool.GetOrCreate(ctx, pod.Namespace, pod.Name, containerName, opts)
	if err != nil {
		icolor.Error("日志流打开失败 [%s/%s/%s]: %v", pod.Namespace, pod.Name, containerName, err)
		return
	}

	var entries []*cache.LogEntry
	lineNum := 0
	scanner := bufio.NewScanner(logStream.Stream)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		lineNum++
		logStream.LinesRead++
		logStream.BytesRead += int64(len(line))

		// Pipeline处理
		logLine := &pipeline.LogLine{
			Raw:           line,
			Namespace:     pod.Namespace,
			PodName:       pod.Name,
			ContainerName: containerName,
		}

		result := pipe.Process(logLine)
		if result == nil || result.Filtered {
			continue
		}

		// JSON查询
		if jq != nil {
			if jsonquery.IsJSON(line) {
				if !jq.Match(line) {
					continue
				}
				line = jq.FormatLine(line)
			}
		}

		// 构建缓存条目
		entry := &cache.LogEntry{
			Namespace:     pod.Namespace,
			PodName:       pod.Name,
			ContainerName: containerName,
			Line:          line,
			LineNumber:    lineNum,
			Level:         detectLogLevel(line),
		}
		entries = append(entries, entry)

		// Trace聚合
		traceAgg.AddLogEntry(entry)

		// 彩色输出
		printer.PrintLog(pod.Namespace, pod.Name, containerName, line)
	}

	// 缓存日志
	if len(entries) > 0 {
		cacheKey := cache.GenerateKey(pod.Namespace, pod.Name, containerName, 0)
		logCache.Put(cacheKey, entries)
	}
}

// buildPipeline 构建处理管道
func buildPipeline() *pipeline.Pipeline {
	pipe := pipeline.NewPipeline()

	// Grep过滤
	if grep != "" {
		pipe.AddStage(pipeline.NewGrepStage(grep, grepInvert, grepIgnoreCase))
	}

	// 日志级别过滤
	if level != "" {
		levels := strings.Split(level, ",")
		pipe.AddStage(pipeline.NewLevelFilterStage(levels))
	}

	return pipe
}

// buildJSONQuery 构建JSON查询
func buildJSONQuery() *jsonquery.Query {
	if jsonFields == "" && len(jsonQuery) == 0 {
		return nil
	}

	jq := jsonquery.NewQuery()

	// 字段选择
	if jsonFields != "" {
		fields := strings.Split(jsonFields, ",")
		for _, f := range fields {
			jq.Select(strings.TrimSpace(f))
		}
		jq.SetFormat(jsonquery.FormatFields)
	}

	// 条件过滤
	for _, q := range jsonQuery {
		cond, err := jsonquery.ParseConditionString(q)
		if err != nil {
			icolor.Warn("忽略无效的JSON查询条件: %s", q)
			continue
		}
		jq.Where(cond.Field, cond.Operator, cond.Value)
	}

	// 输出格式
	switch output {
	case "flat":
		jq.SetFormat(jsonquery.FormatFlat)
	case "table":
		jq.SetFormat(jsonquery.FormatTable)
	case "fields":
		jq.SetFormat(jsonquery.FormatFields)
	}

	return jq
}

// buildLogOptions 构建日志选项
func buildLogOptions() *corev1.PodLogOptions {
	opts := &corev1.PodLogOptions{
		Follow:     follow,
		Previous:   previous,
		Timestamps: timestamps,
	}

	if tail >= 0 {
		opts.TailLines = &tail
	}

	if since != "" {
		d, err := time.ParseDuration(since)
		if err == nil {
			seconds := int64(d.Seconds())
			opts.SinceSeconds = &seconds
		}
	}

	return opts
}

// resolveNamespaces 解析namespace列表
func resolveNamespaces(ctx context.Context, client *kubernetes.Client) ([]string, error) {
	if allNamespaces {
		ns, err := client.GetAllNamespaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取namespace列表失败: %w", err)
		}
		return ns, nil
	}

	return []string{client.Namespace}, nil
}

// detectLogLevel 探测日志级别
func detectLogLevel(line string) string {
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, "ERROR") || strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC"):
		return "ERROR"
	case strings.Contains(upper, "WARN"):
		return "WARN"
	case strings.Contains(upper, "INFO"):
		return "INFO"
	case strings.Contains(upper, "DEBUG"):
		return "DEBUG"
	case strings.Contains(upper, "TRACE"):
		return "TRACE"
	default:
		return ""
	}
}
