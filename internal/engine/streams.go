package engine

import (
	"bufio"
	"context"
	"fmt"
	"sync"

	"klog/internal/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// StreamManager 流管家：管理所有 TCP 日志长连接的生命周期
type StreamManager struct {
	ctx       context.Context
	clientset kubernetes.Interface
	registry  map[string]*LogStream
	mu        sync.RWMutex
	maxSize   int
}

// LogStream 单个日志流容器
type LogStream struct {
	Namespace string
	PodName   string
	Container string
	Scanner   *bufio.Scanner
	Cancel    context.CancelFunc
	Active    bool
}

// NewStreamManager 创建流管家
func NewStreamManager(ctx context.Context, clientset kubernetes.Interface, maxSize int) *StreamManager {
	return &StreamManager{
		ctx:       ctx,
		clientset: clientset,
		registry:  make(map[string]*LogStream),
		maxSize:   maxSize,
	}
}

// GetOrCreate 获取或新建流
func (m *StreamManager) GetOrCreate(ns, pod, container string, cfg *config.Config) (*LogStream, error) {
	key := fmt.Sprintf("%s/%s/%s", ns, pod, container)

	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. 防止重复挂载 (Avoid Duplicated Streams)
	if s, ok := m.registry[key]; ok && s.Active {
		return s, nil
	}

	// 2. 建立新连接 (绑定到 Root Context)
	opts := &corev1.PodLogOptions{
		Container:  container,
		Follow:     cfg.Follow,
		Timestamps: cfg.Timestamps,
		Previous:   cfg.Previous,
	}
	if cfg.Tail >= 0 {
		opts.TailLines = &cfg.Tail
	}

	// 这里的 ctx 必须是派生自 root context，以便一键关停
	streamCtx, cancel := context.WithCancel(m.ctx)
	req := m.clientset.CoreV1().Pods(ns).GetLogs(pod, opts)
	rc, err := req.Stream(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}

	s := &LogStream{
		Namespace: ns,
		PodName:   pod,
		Container: container,
		Scanner:   bufio.NewScanner(rc),
		Cancel:    cancel,
		Active:    true,
	}

	m.registry[key] = s

	// 如果连接断开，自动更新状态
	go func() {
		<-streamCtx.Done()
		m.mu.Lock()
		s.Active = false
		rc.Close()
		m.mu.Unlock()
	}()

	return s, nil
}

// CloseAll 关闭所有由管家持有的流 (由 Engine 调用)
func (m *StreamManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.registry {
		if s.Cancel != nil {
			s.Cancel()
		}
	}
}
