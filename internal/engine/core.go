package engine

import (
	"context"
	"fmt"
	"sync"

	icolor "klog/internal/color"
	"klog/internal/config"
	"klog/internal/k8s"

	corev1 "k8s.io/api/core/v1"
)

// Engine 系统大脑 (Everything Container)
type Engine struct {
	ctx context.Context
	cfg *config.Config

	// Adapters & Managers
	k8s      k8s.Adapter
	streams  *StreamManager
	informer *k8s.PodInformer
	pipeline *Pipeline

	mu      sync.Mutex
	started bool
}

// New 创建并初始化所有子系统，绑定到唯一的 root context
func New(ctx context.Context, cfg *config.Config) (*Engine, error) {
	// 1. 初始化 K8s Adapter
	adapter, err := k8s.NewAdapter(cfg.Kubeconfig, cfg.Namespace)
	if err != nil {
		return nil, fmt.Errorf("k8s 适配器初始化失败: %w", err)
	}

	// 2. 初始化子系统
	e := &Engine{
		ctx:      ctx,
		cfg:      cfg,
		k8s:      adapter,
		streams:  NewStreamManager(ctx, adapter.Clientset(), 100),
		informer: k8s.NewPodInformer(cfg.Kubeconfig, cfg.Namespace), // Informer 独立初始化或注入
		pipeline: NewPipeline(),
	}

	return e, nil
}

// ProcessStream 实现日志处理流
func (e *Engine) ProcessStream(ls *LogStream) {
	scanner := ls.Scanner
	printer := icolor.NewPrinter()

	for scanner.Scan() {
		line := scanner.Text()

		// 构建 LogLine 对象传递给 Pipeline
		logLine := &LogLine{
			Raw:           line,
			Namespace:     ls.Namespace,
			PodName:       ls.PodName,
			ContainerName: ls.Container,
		}

		processed := e.pipeline.Process(logLine)
		if processed != nil && !processed.Filtered {
			printer.PrintLog(ls.Namespace, ls.PodName, ls.Container, processed.Raw)
		}
	}
}

// ExecuteLogs 执行日志查询核心逻辑
func (e *Engine) ExecuteLogs(resource string) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("引擎已启动")
	}
	e.started = true
	e.mu.Unlock()

	icolor.Header("klog Engine 启动 (Context ID: %p)", e.ctx)

	// 1. 启动状态感知
	if err := e.informer.Start(e.ctx); err != nil {
		return err
	}

	// 2. 资源解析 (通过 Adapter)
	pods, err := e.k8s.ResolvePods(e.ctx, resource)
	if err != nil {
		return err
	}

	icolor.Info("找到 %d 个目标 Pod, 建立初始连接池...", len(pods))

	// 3. 初始集群日志挂载 (Fan-out)
	for _, p := range pods {
		e.AttachPod(p)
	}

	// 4. 实现动态集群观测：Rolling Update 自动感知
	if e.cfg.Follow {
		go e.watchClusterChanges(resource)
	}

	// 5. 统一生命周期阻塞
	<-e.ctx.Done()
	icolor.Warn("接收到停止信号, 正在回收所有连接...")
	return nil
}

// AttachPod 挂载单个 Pod
func (e *Engine) AttachPod(pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		// 过滤器逻辑可在此切入
		go e.startStream(pod.Namespace, pod.Name, container.Name)
	}
}

// startStream 启动日志流并注入管道
func (e *Engine) startStream(ns, pod, container string) {
	ls, err := e.streams.GetOrCreate(ns, pod, container, e.cfg)
	if err != nil {
		return
	}

	e.ProcessStream(ls)
}

// watchClusterChanges 动态集群追踪的核心
func (e *Engine) watchClusterChanges(targetResource string) {
	events := e.informer.Events()
	for {
		select {
		case <-e.ctx.Done():
			return
		case event := <-events:
			// 如果是新创建的 Pod，且根据命名/标签符合 targetResource 规则
			if event.Type == "add" && event.Pod.Status.Phase == corev1.PodRunning {
				//TODO: 此处调用 k8s.Resolver 做细粒度匹配
				icolor.Success("🔥 集群扩容/滚动更新检测到新 Pod: %s", event.Pod.Name)
				e.AttachPod(event.Pod)
			}
		}
	}
}
