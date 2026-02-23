package engine

import (
	"context"
	"fmt"

	"klog/internal/color"
	"klog/kubernetes"

	corev1 "k8s.io/api/core/v1"
)

// Engine 核心日志引擎
type Engine struct {
	ctx        context.Context
	cancel     context.CancelFunc
	client     *kubernetes.Client
	informer   *kubernetes.PodInformer
	resolver   *kubernetes.Resolver
	streamPool *StreamPool
	pipeline   *Pipeline

	// 配置项
	Namespace string
	Selectors string
	Follow    bool
}

// NewEngine 创建并初始化引擎
func NewEngine(ctx context.Context, kubeconfig, ns string) (*Engine, error) {
	client, err := kubernetes.NewClient(kubeconfig, ns)
	if err != nil {
		return nil, err
	}

	eCtx, eCancel := context.WithCancel(ctx)

	e := &Engine{
		ctx:        eCtx,
		cancel:     eCancel,
		client:     client,
		informer:   kubernetes.NewPodInformer(client, ns),
		resolver:   kubernetes.NewResolver(client),
		streamPool: NewStreamPool(client.Clientset, 100),
		pipeline:   NewPipeline(),
		Namespace:  ns,
	}

	return e, nil
}

// Start 启动引擎（初始化 Informer 和事件监听）
func (e *Engine) Start() error {
	color.Info("🚀 启动 klog Engine...")

	// 1. 启动 Informer
	if e.Selectors != "" {
		e.informer.SetLabelSelector(e.Selectors)
	}
	if err := e.informer.Start(e.ctx); err != nil {
		return fmt.Errorf("启动 Informer 失败: %w", err)
	}

	// 2. 联动动态追踪：当新 Pod 出现时自动 Attach 日志流
	if e.Follow {
		go e.watchPodChanges()
	}

	return nil
}

// Stop 停止引擎
func (e *Engine) Stop() {
	color.Info("🛑 停止引擎...")
	e.cancel()
	e.streamPool.CloseAll()
}

// watchPodChanges 监听 Pod 变化，实现滚动更新自动切换
func (e *Engine) watchPodChanges() {
	events := e.informer.Events()
	for {
		select {
		case <-e.ctx.Done():
			return
		case event := <-events:
			// 核心逻辑：如果是新增加的 Pod，且符合我们的选择器，自动开启日志流
			if event.Type == "add" && event.Pod.Status.Phase == corev1.PodRunning {
				// 这里可以根据当前正在追踪的资源（如 Deployment）进行过滤
				// 暂时简化处理：如果是 Running 状态则尝试 Attach
				go e.AttachPod(event.Pod)
			}
			// 如果 Pod 被删除，StreamPool 会在下次读取失败或通过 cleanup 回收
		}
	}
}

// AttachPod 为单个 Pod 开启日志流
func (e *Engine) AttachPod(pod *corev1.Pod) {
	// 避免重复 attach
	for _, container := range pod.Spec.Containers {
		color.Success("✨ 发现新 Pod: %s/%s, 自动挂载日志流...", pod.Namespace, pod.Name)

		opts := &corev1.PodLogOptions{
			Container:  container.Name,
			Follow:     true,
			Timestamps: true,
		}

		// 获取或创建流并开始处理
		logStream, err := e.streamPool.GetOrCreate(e.ctx, pod.Namespace, pod.Name, container.Name, opts)
		if err != nil {
			continue
		}

		go e.ProcessStream(logStream)
	}
}

// ProcessStream 处理日志流（输出、Pipeline等）
func (e *Engine) ProcessStream(ls *LogStream) {
	printer := color.NewPrinter()
	scanner := ls.Scanner

	for scanner.Scan() {
		line := scanner.Text()

		// 通过 Pipeline 处理
		logLine := &LogLine{
			Raw:           line,
			Namespace:     ls.Namespace,
			PodName:       ls.PodName,
			ContainerName: ls.ContainerName,
		}

		processed := e.pipeline.Process(logLine)
		if processed != nil && !processed.Filtered {
			printer.PrintLog(ls.Namespace, ls.PodName, ls.ContainerName, processed.Raw)
		}
	}
}

// GetClientGetter 获取资源解析器（基于 Cache）
func (e *Engine) Resolver() *kubernetes.Resolver {
	return e.resolver
}

// ListPodsFromCache 从本地 Informer 缓存获取 Pod
func (e *Engine) ListPodsFromCache() ([]*corev1.Pod, error) {
	return e.informer.ListPods()
}
