package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"klog/engine"
	icolor "klog/internal/color"

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

示例：
  klog logs deploy/nginx                       # 查看Deployment日志
  klog logs nginx                              # 自动推断资源类型
  klog logs deploy/myapp -f --grep "error"     # 流式查看并在滚动更新时自动追踪
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
		cancel()
	}()

	// 1. 初始化引擎
	eng, err := engine.NewEngine(ctx, kubeconfig, namespace)
	if err != nil {
		return err
	}
	defer eng.Stop()

	eng.Follow = follow
	if labelSelector != "" {
		eng.Selectors = labelSelector
	}

	// 2. 启动引擎（初始化 Informer 缓存）
	if err := eng.Start(); err != nil {
		return err
	}

	// 3. 资源解析（现在走本地缓存了！）
	ref := eng.Resolver().ParseResource(args[0])
	icolor.Header("正在解析资源: %s/%s", ref.Type, ref.Name)

	// 从缓存中获取 Pod 列表
	pods, err := eng.Resolver().ResolveToPods(ctx, ref, namespace)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		return fmt.Errorf("未找到匹配的 Pod")
	}

	icolor.Header("找到 %d 个 Pod, 开始追踪日志...", len(pods))

	// 4. 为初始发现的 Pods 开启流
	fullPods, _ := eng.ListPodsFromCache()
	for _, p := range pods {
		for _, fp := range fullPods {
			if fp.Name == p.Name {
				go eng.AttachPod(fp)
				break
			}
		}
	}

	// 5. 阻塞等待，直到 context 取消
	<-ctx.Done()
	return nil
}

// 以下辅助函数由 engine 内部逻辑接管，此文件中暂时保留 buildPipeline 以供后期自定义配置扩展
func buildPipeline() *engine.Pipeline {
	pipe := engine.NewPipeline()
	if grep != "" {
		pipe.AddStage(engine.NewGrepStage(grep, grepInvert, grepIgnoreCase))
	}
	if level != "" {
		levels := []string{level}
		pipe.AddStage(engine.NewLevelFilterStage(levels))
	}
	return pipe
}

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
		d, _ := time.ParseDuration(since)
		seconds := int64(d.Seconds())
		opts.SinceSeconds = &seconds
	}
	return opts
}
