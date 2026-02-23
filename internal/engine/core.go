package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	icolor "klog/internal/color"
	"klog/internal/config"
	"klog/internal/k8s"
)

// Stats 引擎自观测指标
type Stats struct {
	TotalStreams      int32
	ActiveStreams     int32
	Goroutines        int32
	DroppedLines      int64
	TotalLines        int64
	ProcessedLines    int64
	ReconnectAttempts int64
}

// Engine 系统内核 (Kernel V5 - The Module Orchestrator)
type Engine struct {
	ctx   context.Context
	cfg   *config.Config
	state int32 // 使用 atomic 操作 State

	// 系统底座
	k8s      k8s.Adapter
	streams  *StreamManager
	informer *k8s.PodInformer
	bus      *EventBus

	// 模组管理
	modules map[string]Module

	// 统计与监控
	stats Stats
	wg    sync.WaitGroup
	mu    sync.RWMutex
}

// New 初始化上帝容器
func New(ctx context.Context, cfg *config.Config) (*Engine, error) {
	// 使用全局 Provider 确保 Transport 复用
	provider, err := k8s.GetProvider(cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}

	adapter, err := k8s.NewAdapter(cfg.Kubeconfig, cfg.Namespace) // 后续可重构为接收 Clientset
	if err != nil {
		return nil, err
	}

	e := &Engine{
		ctx:      ctx,
		cfg:      cfg,
		state:    int32(StateInit),
		k8s:      adapter,
		streams:  NewStreamManager(ctx, provider.Clientset, 500),
		informer: k8s.NewPodInformer(cfg.Kubeconfig, cfg.Namespace),
		bus:      NewEventBus(),
		modules:  make(map[string]Module),
	}

	e.Spawn(e.eventRouter)
	e.Spawn(e.statsMonitor) // 启动自观测控制台

	return e, nil
}

func (e *Engine) statsMonitor(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.StatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			icolor.Header("--- KLOG ENGINE STATS ---")
			icolor.Info("Active Streams: %d | Goroutines: %d",
				atomic.LoadInt32(&e.stats.ActiveStreams),
				atomic.LoadInt32(&e.stats.Goroutines))
			icolor.Info("Processed: %d | Dropped: %d | Reconnects: %d",
				atomic.LoadInt64(&e.stats.ProcessedLines),
				atomic.LoadInt64(&e.stats.DroppedLines),
				atomic.LoadInt64(&e.stats.ReconnectAttempts))
		}
	}
}

func (e *Engine) eventRouter(ctx context.Context) {
	events := e.informer.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			// 将 K8s 原始事件包装后发布到系统总线
			e.bus.Publish("pod_events", Event{
				Type: ev.Type,
				Data: ev.Pod,
			})
		}
	}
}

// Events 暴露消息中心 (供 Module 订阅)
func (e *Engine) Events() *EventBus { return e.bus }

// Start 启动引擎与其下所有模组
func (e *Engine) Start() error {
	if !atomic.CompareAndSwapInt32(&e.state, int32(StateInit), int32(StateRunning)) {
		return fmt.Errorf("引擎无法启动: 当前状态 %v", e.state)
	}

	icolor.Header("klog v5 Kernel Starting...")

	// 启动核心基础组件
	if err := e.informer.Start(e.ctx); err != nil {
		return err
	}

	// 启动业务模组
	for _, m := range e.modules {
		if err := m.Start(); err != nil {
			return err
		}
	}

	return nil
}

// Stop 优雅关停
func (e *Engine) Stop() {
	if !atomic.CompareAndSwapInt32(&e.state, int32(StateRunning), int32(StateStopping)) {
		return
	}

	icolor.Warn("Kernel: 执行关停指令...")

	// 1. 停止模组 (业务层)
	for _, m := range e.modules {
		_ = m.Stop()
	}

	// 2. 停止长连接 (通信层)
	e.streams.CloseAll()

	// 3. 停止基础组件 (底座层)
	e.informer.Stop()

	// 4. 等待所有派生协程归零
	e.wg.Wait()

	atomic.StoreInt32(&e.state, int32(StateStopped))
	icolor.Success("Kernel: 系统已完全停止 (Stats: Dropped=%d)", atomic.LoadInt64(&e.stats.DroppedLines))
}

// AttachModule 挂载功能模组
func (e *Engine) AttachModule(m Module) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := m.Init(e.ctx, e); err != nil {
		return err
	}
	e.modules[m.Name()] = m
	return nil
}

// Spawn 统一协程派生 (具备 WaitGroup 约束)
func (e *Engine) Spawn(f func(ctx context.Context)) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		f(e.ctx)
	}()
}

// Internal Accessors (仅限内部模块调用)
func (e *Engine) Adapter() k8s.Adapter       { return e.k8s }
func (e *Engine) Streams() *StreamManager    { return e.streams }
func (e *Engine) Informer() *k8s.PodInformer { return e.informer }
func (e *Engine) Config() *config.Config     { return e.cfg }
