package config

import (
	"time"

	"github.com/spf13/cobra"
)

// Config 核心配置结构体
type Config struct {
	// K8s 集群配置
	Kubeconfig string
	Namespace  string
	AllNS      bool

	// 查询过滤配置
	Container      string
	LabelSelector  string
	Grep           string
	GrepInvert     bool
	GrepIgnoreCase bool
	Level          []string
	Since          time.Duration
	Tail           int64
	Previous       bool
	Timestamps     bool

	// 输出配置
	Output    string
	Highlight []string
	JsonQuery []string

	// 处理选项
	Follow  bool
	Verbose bool

	// 性能调优 (V5.4+)
	SortWindow    time.Duration
	StatsInterval time.Duration
}

// NewDefaultConfig 返回默认配置
func NewDefaultConfig() *Config {
	return &Config{
		Tail:          -1,
		SortWindow:    200 * time.Millisecond,
		StatsInterval: 10 * time.Second,
	}
}

// BindFlags 将 Cobra Flags 绑定到 Config 结构体
func (c *Config) BindFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	pf := cmd.PersistentFlags()

	// 全局/持久化 Flags
	pf.StringVar(&c.Kubeconfig, "kubeconfig", "", "kubeconfig 文件路径")
	pf.StringVarP(&c.Namespace, "namespace", "n", "", "指定 namespace")
	pf.BoolVarP(&c.AllNS, "all-namespaces", "A", false, "工作在所有 namespace")
	pf.BoolVarP(&c.Verbose, "verbose", "v", false, "显示详细调试日志")

	// 仅在 logs 命令中使用的 Flags (按需绑定)
	if cmd.Name() == "logs" || cmd.Name() == "trace" {
		f.BoolVarP(&c.Follow, "follow", "f", false, "流式跟踪日志")
		f.StringVarP(&c.Container, "container", "c", "", "指定容器名")
		f.StringVarP(&c.LabelSelector, "selector", "l", "", "标签选择器")
		// ... 其它具体命令的 flags 可以在命令初始化时动态绑定
	}
}
