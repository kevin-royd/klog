package engine

import (
	"container/heap"
	"context"
	"time"

	icolor "klog/internal/color"

	corev1 "k8s.io/api/core/v1"
)

// LogHeap 用于时间排序的优先队列
type LogHeap []*LogLine

func (h LogHeap) Len() int            { return len(h) }
func (h LogHeap) Less(i, j int) bool  { return h[i].Timestamp.Before(h[j].Timestamp) }
func (h LogHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *LogHeap) Push(x interface{}) { *h = append(*h, x.(*LogLine)) }
func (h *LogHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// LogsModule 工业级日志模组 V5+
type LogsModule struct {
	ctx      context.Context
	engine   *Engine
	resource string
	logChan  chan *LogLine
}

func NewLogsModule(resource string) *LogsModule {
	return &LogsModule{
		resource: resource,
		logChan:  make(chan *LogLine, 2000),
	}
}

func (m *LogsModule) Name() string { return "Logs" }

func (m *LogsModule) Init(ctx context.Context, e *Engine) error {
	m.ctx = ctx
	m.engine = e
	return nil
}

func (m *LogsModule) Start() error {
	// 启动全域排序渲染引擎
	m.engine.Spawn(m.sortedRenderer)

	// 启动 Pod 路由观察
	m.engine.Spawn(m.watchAdapter)

	// 后续初始扫一遍
	pods, _ := m.engine.Adapter().ResolvePods(m.ctx, m.resource)
	for _, p := range pods {
		m.attachPod(p)
	}
	return nil
}

func (m *LogsModule) Stop() error { return nil }

// sortedRenderer: 解决乱序的核心
func (m *LogsModule) sortedRenderer(ctx context.Context) {
	printer := icolor.NewPrinter()
	h := &LogHeap{}
	heap.Init(h)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case line := <-m.logChan:
			heap.Push(h, line)
			// 配置：防止 Heap 无限增长 (溢出保护)
			if h.Len() > 5000 {
				line := heap.Pop(h).(*LogLine)
				printer.PrintLog(line.Namespace, line.PodName, line.ContainerName, line.Raw)
			}
		case <-ticker.C:
			now := time.Now()
			window := m.engine.Config().SortWindow
			for h.Len() > 0 {
				top := (*h)[0]
				if now.Sub(top.Timestamp) < window {
					break
				}
				line := heap.Pop(h).(*LogLine)
				printer.PrintLog(line.Namespace, line.PodName, line.ContainerName, line.Raw)
			}
		}
	}
}

func (m *LogsModule) watchAdapter(ctx context.Context) {
	// 订阅系统总线，不再直连 Watcher
	events := m.engine.Events().Subscribe("pod_events")
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			pod := ev.Data.(*corev1.Pod)
			if ev.Type == "added" {
				m.attachPod(pod)
			}
		}
	}
}

func (m *LogsModule) attachPod(pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		m.engine.Spawn(func(ctx context.Context) {
			m.streamWorker(pod.Namespace, pod.Name, container.Name)
		})
	}
}
