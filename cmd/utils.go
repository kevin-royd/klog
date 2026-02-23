package cmd

import (
	"context"
	"fmt"
	"strings"

	"klog/kubernetes"
)

// resolveNamespaces 解析 namespace 列表 (共享于各命令)
func resolveNamespaces(ctx context.Context, client *kubernetes.Client) ([]string, error) {
	if allNamespaces {
		ns, err := client.GetAllNamespaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取 namespace 列表失败: %w", err)
		}
		return ns, nil
	}

	return []string{client.Namespace}, nil
}

// detectLogLevel 探测日志级别 (共享于各命令)
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
