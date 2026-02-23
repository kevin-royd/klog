package engine

import (
	"context"
	"sync/atomic"

	icolor "klog/internal/color"

	corev1 "k8s.io/api/core/v1"
)

// LogsModule 日志模组
type LogsModule struct {
	ctx      context.Context
	engine   *Engine
	resource string
	pipeline *Pipeline

	// Renderer Backpressure
	logChan chan *LogLine
}

func NewLogsModule(resource string) *LogsModule {
	return &LogsModule{
		resource: resource,
		pipeline: NewPipeline(),
		logChan:  make(chan *LogLine, 1000), // 有界缓冲
	}
}

func (m *LogsModule) Name() string { return "Logs" }

func (m *LogsModule) Init(ctx context.Context, e *Engine) error {
	m.ctx = ctx
	m.engine = e
	return nil
}

func (m *LogsModule) Start() error {
	// 1. 启动异步渲染协程 (Zero-Blocking Renderer)
	m.engine.Spawn(m.renderer)

	// 2. 初始解析与挂载
	pods, err := m.engine.Adapter().ResolvePods(m.ctx, m.resource)
	if err != nil {
		return err
	}

	for _, p := range pods {
		m.attachPod(p)
	}

	// 3. 动态观测
	if m.engine.Config().Follow {
		m.engine.Spawn(m.watchLoop)
	}
	return nil
}

func (m *LogsModule) Stop() error { return nil }

func (m *LogsModule) renderer(ctx context.Context) {
	printer := icolor.NewPrinter()
	for {
		select {
		case <-ctx.Done():
			return
		case line := <-m.logChan:
			printer.PrintLog(line.Namespace, line.PodName, line.ContainerName, line.Raw)
		}
	}
}

func (m *LogsModule) watchLoop(ctx context.Context) {
	events := m.engine.Informer().Events()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			if event.Type == "add" && event.Pod.Status.Phase == corev1.PodRunning {
				m.attachPod(event.Pod)
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

func (m *LogsModule) streamWorker(ns, pod, container string) {
	ls, err := m.engine.Streams().GetOrCreate(ns, pod, container, m.engine.Config())
	if err != nil {
		return
	}

	for ls.Scanner.Scan() {
		// 丢包策略 (Drop-on-Busy)
		select {
		case <-m.ctx.Done():
			return
		case m.logChan <- &LogLine{
			Raw:           ls.Scanner.Text(),
			Namespace:     ls.Namespace,
			PodName:       ls.PodName,
			ContainerName: ls.Container,
		}:
			// 成功送入
		default:
			// 渲染队列满了，丢弃并计数
			atomic.AddInt64(&m.engine.stats.DroppedLines, 1)
		}
	}
}
