package cmd

import (
	"context"
	"fmt"
	"strings"

	"klog/internal/k8s"
)

// resolveNamespaces 解析 namespace 列表 (基于 Adapter)
func resolveNamespaces(ctx context.Context, adapter k8s.Adapter) ([]string, error) {
	if logCfg.AllNS {
		ns, err := adapter.GetAllNamespaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取 namespace 列表失败: %w", err)
		}
		return ns, nil
	}

	return []string{adapter.GetNamespace()}, nil
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
