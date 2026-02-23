package engine

import (
	"context"
	"fmt"
	"sync"

	icolor "klog/internal/color"
	"klog/internal/config"
	"klog/kubernetes"

	corev1 "k8s.io/api/core/v1"
)

// Engine 核心日志业务逻辑
type Engine struct {
	cfg        *config.Config
	kubeClient kubernetes.K8sClient // 依赖接口 (DI)
	informer   *kubernetes.PodInformer
	streamPool *StreamPool
	pipeline   *Pipeline

	ctx context.Context
}

// NewEngine 使用 Config 和 Client 接口初始化
func NewEngine(ctx context.Context, cfg *config.Config, client kubernetes.K8sClient) *Engine {
	return &Engine{
		ctx:        ctx,
		cfg:        cfg,
		kubeClient: client,
		informer:   kubernetes.NewPodInformer(client.(*kubernetes.Client), cfg.Namespace),
		streamPool: NewStreamPool(client.GetClientset(), 100),
		pipeline:   NewPipeline(),
	}
}

// RunLogs 启动日志追踪业务
func (e *Engine) RunLogs(resource string) error {
	// 1. 启动 Informer (准备本地缓存)
	if err := e.informer.Start(e.ctx); err != nil {
		return err
	}

	// 2. 解析资源到 Pods (利用同步 Engine 思维)
	resolver := kubernetes.NewResolver(e.kubeClient.(*kubernetes.Client))
	ref := resolver.ParseResource(resource)

	pods, err := resolver.ResolveToPods(e.ctx, ref, e.cfg.Namespace)
	if err != nil {
		return err
	}

	// 3. 多容器/多 Pod 自动聚合聚合 (Killer Feature: Auto-Fan-out)
	var wg sync.WaitGroup
	for _, p := range pods {
		// 从 Informer 拿 full object
		fullPod, err := e.informer.GetPod(p.Namespace, p.Name)
		if err != nil {
			continue
		}

		wg.Add(1)
		go func(pod *corev1.Pod) {
			defer wg.Done()
			e.AttachPod(pod)
		}(fullPod)
	}

	// 4. 实现动态 watch：如果 e.cfg.Follow 为真，监听新增 Pod
	if e.cfg.Follow {
		go e.watchForNewPods(ref)
	}

	// 阻塞直到 Context 取消 (Ctrl+C)
	<-e.ctx.Done()
	wg.Wait()
	return nil
}

// watchForNewPods 解决 Pod 重建断流问题
func (e *Engine) watchForNewPods(ref kubernetes.ResourceRef) {
	events := e.informer.Events()
	for {
		select {
		case <-e.ctx.Done():
			return
		case event := <-events:
			if event.Type == "add" && event.Pod.Status.Phase == corev1.PodRunning {
				// 匹配资源所属 (如果是 Deployment 滚动更新，新 Pod 会进来)
				if e.matchResource(event.Pod, ref) {
					go e.AttachPod(event.Pod)
				}
			}
		}
	}
}

// matchResource 判断 Pod 是否属于目标资源
func (e *Engine) matchResource(pod *corev1.Pod, ref kubernetes.ResourceRef) bool {
	// 简单的名称前缀匹配（实际可基于 OwnerReference 做得更精准）
	return ref.Name == "" || (pod.Namespace == ref.Namespace && (pod.Name == ref.Name || fmt.Sprintf("%s", pod.GenerateName) == ref.Name))
}

// ProcessStream 处理日志流
func (e *Engine) ProcessStream(ls *LogStream) {
	scanner := ls.Scanner
	printer := icolor.NewPrinter()

	for scanner.Scan() {
		line := scanner.Text()

		// 1. 调用 Pipeline
		logLine := &LogLine{
			Raw:           line,
			Namespace:     ls.Namespace,
			PodName:       ls.PodName,
			ContainerName: ls.ContainerName,
		}

		processed := e.pipeline.Process(logLine)
		if processed != nil && !processed.Filtered {
			// 2. 彩色输出
			printer.PrintLog(ls.Namespace, ls.PodName, ls.ContainerName, processed.Raw)
		}
	}
}

// AttachPod 启动日志流
func (e *Engine) AttachPod(pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		opts := &corev1.PodLogOptions{
			Container:  container.Name,
			Follow:     e.cfg.Follow,
			Timestamps: e.cfg.Timestamps,
		}
		if e.cfg.Tail >= 0 {
			opts.TailLines = &e.cfg.Tail
		}

		ls, err := e.streamPool.GetOrCreate(e.ctx, pod.Namespace, pod.Name, container.Name, opts)
		if err != nil {
			continue
		}

		e.ProcessStream(ls)
	}
}
