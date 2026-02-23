package engine

import (
	"context"
	"time"
)

// State 引擎状态 (原子保护)
type State int32

const (
	StateInit     State = 0
	StateRunning  State = 1
	StateStopping State = 2
	StateStopped  State = 3
)

// Module 插件化模组接口
type Module interface {
	Name() string
	Init(ctx context.Context, e *Engine) error
	Start() error
	Stop() error
}

// LogLine 领域模型
type LogLine struct {
	Raw           string
	Namespace     string
	PodName       string
	ContainerName string
	Timestamp     time.Time
	Fields        map[string]string // 供解析器使用的 KV
	Filtered      bool
}
