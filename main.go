package main

import (
	"fmt"
	"klog/cmd"
	"os"
)

// klog - Kubernetes Log Observability CLI
// 支持作为独立工具使用，也支持作为 kubectl 插件 (kubectl klog)
func main() {
	if err := cmd.Execute(); err != nil {
		// 这里不再需要 os.Exit，因为 cmd.Execute 内部已经处理了错误并返回
		fmt.Printf("\n❌ 发生错误: %v\n", err)
		os.Exit(1)
	}
}
