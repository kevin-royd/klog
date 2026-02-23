package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	kubeconfig     string
	namespace      string
	allNamespaces  bool
	container      string
	labelSelector  string
	since          string
	tail           int64
	follow         bool
	grep           string
	grepInvert     bool
	grepIgnoreCase bool
	previous       bool
	timestamps     bool
	output         string
	jsonFields     string
	jsonQuery      []string
	highlight      []string
	level          string
)

var rootCmd = &cobra.Command{
	Use:   "klog",
	Short: "klog - 高效 Kubernetes 日志查询工具",
	Long: `
██╗  ██╗██╗      ██████╗  ██████╗ 
██║ ██╔╝██║     ██╔═══██╗██╔════╝ 
█████╔╝ ██║     ██║   ██║██║  ███╗
██╔═██╗ ██║     ██║   ██║██║   ██║
██║  ██╗███████╗╚██████╔╝╚██████╔╝
╚═╝  ╚═╝╚══════╝ ╚═════╝  ╚═════╝

klog 是一个增强的 kubectl logs 工具，提供：

  ● 跨 Namespace 搜索日志
  ● Deployment/StatefulSet/Service → Pod 自动解析
  ● Pod Watch 实时监控
  ● 彩色日志输出（自动着色）
  ● Log Index Cache（LRU缓存加速查询）
  ● CrashLoopBackOff 自动捕获
  ● Trace ID 自动聚合
  ● JSON 日志结构化查询
  ● Pipeline 日志处理
  ● Log Stream Pool（流连接池）
  ● K8s Informer 高效监控

使用示例：
  klog logs deploy/nginx                  # 查看Deployment的日志
  klog logs nginx -A --grep "error"       # 跨namespace搜索error日志
  klog watch -A                           # 监控所有namespace的Pod
  klog crash -A                           # 捕获所有CrashLoop的Pod
  klog trace deploy/myapp                 # 聚合trace日志
  klog search "myapp" -A                  # 跨namespace搜索Pod
`,
	Version: "1.0.0",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig 文件路径")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "指定 namespace (默认使用当前context)")
	rootCmd.PersistentFlags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "搜索所有 namespace")
	rootCmd.PersistentFlags().StringVarP(&container, "container", "c", "", "指定容器名")
	rootCmd.PersistentFlags().StringVarP(&labelSelector, "selector", "l", "", "标签选择器")
}

// Execute 执行根命令
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	return nil
}
