package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	icolor "klog/internal/color"
	"klog/internal/config"
	"klog/internal/k8s"

	corev1 "k8s.io/api/core/v1"
)

// Stats 引擎运行统计 (自观测)
type Stats struct {
	TotalStreams   int32
	ActiveStreams  int32
	Goroutines     int32
	DroppedLines   int64
	TotalLines     int64
	ProcessedLines int64
}

// Engine 系统内核 (Kernel V4)
type Engine struct {
	ctx context.Context
	cfg *config.Config

	k8s      k8s.Adapter
	streams  *StreamManager
	informer *k8s.PodInformer
	pipeline *Pipeline

	wg     sync.WaitGroup
	mu     sync.Mutex
	status int32 // 0: stopped, 1: running

	// 指标
	stats Stats
}

// New 初始化系统，关联 root context
func New(ctx context.Context, cfg *config.Config) (*Engine, error) {
	adapter, err := k8s.NewAdapter(cfg.Kubeconfig, cfg.Namespace)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		ctx:      ctx,
		cfg:      cfg,
		k8s:      adapter,
		streams:  NewStreamManager(ctx, adapter.Clientset(), 200),
		informer: k8s.NewPodInformer(cfg.Kubeconfig, cfg.Namespace),
		pipeline: NewPipeline(),
	}

	return e, nil
}

// Spawn 统一协程创建入口：实现 Ownership 追踪
func (e *Engine) Spawn(f func(ctx context.Context)) {
	atomic.AddInt32(&e.stats.Goroutines, 1)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer atomic.AddInt32(&e.stats.Goroutines, -1)
		f(e.ctx)
	}()
}

// RunLogs 启动入口 (支持可重复调用)
func (e *Engine) RunLogs(resource string) error {
	e.mu.Lock()
	if e.status == 1 {
		e.mu.Unlock()
		return fmt.Errorf("引擎已在运行中")
	}
	e.status = 1
	e.mu.Unlock()

	icolor.Header("klog V4 Kernel Ready (Status: %d)", e.status)

	// 后续所有逻辑通过 Spawn 派生
	if err := e.informer.Start(e.ctx); err != nil {
		return err
	}

	pods, err := e.k8s.ResolvePods(e.ctx, resource)
	if err != nil {
		return err
	}

	for _, p := range pods {
		e.attachPod(p)
	}

	if e.cfg.Follow {
		e.Spawn(func(ctx context.Context) {
			e.eventLoop(resource)
		})
	}

	// 阻塞等待
	<-e.ctx.Done()
	e.Stop()
	return nil
}

// Stop 绝对关停：状态重置，确保支持 Start/Stop 循环
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status == 0 {
		return
	}

	icolor.Warn("Kernel: 执行关停, 当前状态指标: Goroutines=%d, Dropped=%d",
		atomic.LoadInt32(&e.stats.Goroutines), atomic.LoadInt64(&e.stats.DroppedLines))

	e.streams.CloseAll()
	e.wg.Wait()

	e.status = 0
	icolor.Success("Kernel: 系统已空转，可安全释放或重启。")
}

func (e *Engine) eventLoop(target string) {
	events := e.informer.Events()
	for {
		select {
		case <-e.ctx.Done():
			return
		case event := <-events:
			if event.Type == "add" && event.Pod.Status.Phase == corev1.PodRunning {
				icolor.Success("✨ 扩容检测: %s", event.Pod.Name)
				e.attachPod(event.Pod)
			}
		}
	}
}

func (e *Engine) attachPod(pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		e.Spawn(func(ctx context.Context) {
			e.streamWorker(pod.Namespace, pod.Name, container.Name)
		})
	}
}

func (e *Engine) streamWorker(ns, pod, container string) {
	atomic.AddInt32(&e.stats.ActiveStreams, 1)
	defer atomic.AddInt32(&e.stats.ActiveStreams, -1)

	ls, err := e.streams.GetOrCreate(ns, pod, container, e.cfg)
	if err != nil {
		return
	}

	e.processStream(ls)
}

func (e *Engine) processStream(ls *LogStream) {
	scanner := ls.Scanner
	printer := icolor.NewPrinter()

	for scanner.Scan() {
		select {
		case <-e.ctx.Done():
			return
		default:
			line := scanner.Text()
			atomic.AddInt64(&e.stats.TotalLines, 1)

			logLine := &LogLine{
				Raw:           line,
				Namespace:     ls.Namespace,
				PodName:       ls.PodName,
				ContainerName: ls.Container,
			}

			// 此时可以进行 Pipeline 处理
			processed := e.pipeline.Process(logLine)
			if processed != nil && !processed.Filtered {
				atomic.AddInt64(&e.stats.ProcessedLines, 1)
				printer.PrintLog(ls.Namespace, ls.PodName, ls.Container, processed.Raw)
			}
		}
	}
}

// GetStats 暴露当前的统计数据
func (e *Engine) GetStats() Stats {
	return e.stats
}
