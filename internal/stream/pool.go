package stream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// LogStream 日志流
type LogStream struct {
	PodName       string
	Namespace     string
	ContainerName string
	Stream        io.ReadCloser
	Scanner       *bufio.Scanner
	Cancel        context.CancelFunc
	Active        bool
	CreatedAt     time.Time
	LastRead      time.Time
	BytesRead     int64
	LinesRead     int64
}

// Close 关闭日志流
func (s *LogStream) Close() {
	s.Active = false
	if s.Cancel != nil {
		s.Cancel()
	}
	if s.Stream != nil {
		s.Stream.Close()
	}
}

// StreamPool 日志流连接池
type StreamPool struct {
	mu          sync.RWMutex
	client      kubernetes.Interface
	streams     map[string]*LogStream
	maxSize     int
	idleTimeout time.Duration
}

// NewStreamPool 创建流连接池
func NewStreamPool(client kubernetes.Interface, maxSize int) *StreamPool {
	pool := &StreamPool{
		client:      client,
		streams:     make(map[string]*LogStream),
		maxSize:     maxSize,
		idleTimeout: 5 * time.Minute,
	}

	// 后台清理空闲连接
	go pool.cleanupLoop()

	return pool
}

// streamKey 生成流的唯一key
func streamKey(namespace, podName, containerName string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, podName, containerName)
}

// GetOrCreate 获取或创建日志流
func (p *StreamPool) GetOrCreate(ctx context.Context, namespace, podName, containerName string,
	opts *corev1.PodLogOptions) (*LogStream, error) {

	key := streamKey(namespace, podName, containerName)

	p.mu.Lock()
	defer p.mu.Unlock()

	// 检查已有的流
	if s, ok := p.streams[key]; ok && s.Active {
		s.LastRead = time.Now()
		return s, nil
	}

	// 容量检查，驱逐最旧的流
	for len(p.streams) >= p.maxSize {
		p.evictOldest()
	}

	// 创建新流
	streamCtx, cancel := context.WithCancel(ctx)
	req := p.client.CoreV1().Pods(namespace).GetLogs(podName, opts)
	readCloser, err := req.Stream(streamCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("打开日志流失败 [%s/%s/%s]: %w", namespace, podName, containerName, err)
	}

	stream := &LogStream{
		PodName:       podName,
		Namespace:     namespace,
		ContainerName: containerName,
		Stream:        readCloser,
		Scanner:       bufio.NewScanner(readCloser),
		Cancel:        cancel,
		Active:        true,
		CreatedAt:     time.Now(),
		LastRead:      time.Now(),
	}

	// 设置Scanner缓冲区大小（支持长日志行）
	stream.Scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	p.streams[key] = stream
	return stream, nil
}

// Remove 移除并关闭流
func (p *StreamPool) Remove(namespace, podName, containerName string) {
	key := streamKey(namespace, podName, containerName)

	p.mu.Lock()
	defer p.mu.Unlock()

	if s, ok := p.streams[key]; ok {
		s.Close()
		delete(p.streams, key)
	}
}

// CloseAll 关闭所有流
func (p *StreamPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, stream := range p.streams {
		stream.Close()
		delete(p.streams, key)
	}
}

// ActiveCount 返回活跃流数量
func (p *StreamPool) ActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, s := range p.streams {
		if s.Active {
			count++
		}
	}
	return count
}

// Stats 返回统计信息
func (p *StreamPool) Stats() map[string]StreamStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := make(map[string]StreamStats)
	for key, s := range p.streams {
		stats[key] = StreamStats{
			Active:    s.Active,
			CreatedAt: s.CreatedAt,
			LastRead:  s.LastRead,
			BytesRead: s.BytesRead,
			LinesRead: s.LinesRead,
		}
	}
	return stats
}

// StreamStats 流统计
type StreamStats struct {
	Active    bool
	CreatedAt time.Time
	LastRead  time.Time
	BytesRead int64
	LinesRead int64
}

// evictOldest 驱逐最旧的空闲流
func (p *StreamPool) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, s := range p.streams {
		if oldestKey == "" || s.LastRead.Before(oldestTime) {
			oldestKey = key
			oldestTime = s.LastRead
		}
	}

	if oldestKey != "" {
		if s, ok := p.streams[oldestKey]; ok {
			s.Close()
		}
		delete(p.streams, oldestKey)
	}
}

// cleanupLoop 定期清理空闲流
func (p *StreamPool) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.Lock()
		var toRemove []string
		for key, s := range p.streams {
			if !s.Active || time.Since(s.LastRead) > p.idleTimeout {
				toRemove = append(toRemove, key)
			}
		}
		for _, key := range toRemove {
			if s, ok := p.streams[key]; ok {
				s.Close()
			}
			delete(p.streams, key)
		}
		p.mu.Unlock()
	}
}
