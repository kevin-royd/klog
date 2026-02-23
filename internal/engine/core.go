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

// Engine 系统内核 (Kernel) - 对外黑盒
type Engine struct {
	ctx context.Context
	cfg *config.Config

	// 私有子系统，外部禁止访问
	k8s      k8s.Adapter
	streams  *StreamManager
	informer *k8s.PodInformer
	pipeline *Pipeline

	wg      sync.WaitGroup // 统领所有后台协程
	started bool
	mu      sync.Mutex
}

// New 创建系统容器
func New(ctx context.Context, cfg *config.Config) (*Engine, error) {
	adapter, err := k8s.NewAdapter(cfg.Kubeconfig, cfg.Namespace)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		ctx:      ctx,
		cfg:      cfg,
		k8s:      adapter,
		streams:  NewStreamManager(ctx, adapter.Clientset(), 200), // 流管家
		informer: k8s.NewPodInformer(cfg.Kubeconfig, cfg.Namespace),
		pipeline: NewPipeline(),
	}

	return e, nil
}

// RunLogs 执行日志追踪业务 (唯一入口)
func (e *Engine) RunLogs(resource string) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("引擎已在运行中")
	}
	e.started = true
	e.mu.Unlock()

	// 1. 启动 Informer
	if err := e.informer.Start(e.ctx); err != nil {
		return err
	}

	// 2. 初始解析
	pods, err := e.k8s.ResolvePods(e.ctx, resource)
	if err != nil {
		return err
	}

	// 3. 初始挂载
	for _, p := range pods {
		e.attachPod(p)
	}

	// 4. 解耦事件追踪 (Watcher -> Engine -> Streams)
	if e.cfg.Follow {
		e.wg.Add(1)
		go e.eventLoop(resource)
	}

	// 5. 等待系统生命周期结束
	<-e.ctx.Done()
	e.Stop()
	return nil
}

// Stop 绝对停止：确保所有子系统资源彻底回收
func (e *Engine) Stop() {
	icolor.Warn("Kernel: 正在启动关停程序...")

	// 关闭所有流 (Owner 直接关停)
	e.streams.CloseAll()

	// 等待所有异步任务退出 (如 eventLoop)
	e.wg.Wait()

	icolor.Success("Kernel: 关停完成, goroutine 已清零。")
}

// eventLoop 无线循环监听集群变化，实现业务路由
func (e *Engine) eventLoop(target string) {
	defer e.wg.Done()
	events := e.informer.Events()

	for {
		select {
		case <-e.ctx.Done():
			return
		case event := <-events:
			// 只有符合条件的“增加”事件才触发流挂载
			if event.Type == "add" && event.Pod.Status.Phase == corev1.PodRunning {
				//TODO: 此处后续接入 ResourceMatcher
				icolor.Success("✨ 检测到新 Pod: %s, 自动挂载日志流...", event.Pod.Name)
				e.attachPod(event.Pod)
			}
		}
	}
}

// attachPod 内部调度：私有
func (e *Engine) attachPod(pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		e.wg.Add(1)
		go func(ns, p, c string) {
			defer e.wg.Done()
			e.streamWorker(ns, p, c)
		}(pod.Namespace, pod.Name, container.Name)
	}
}

// streamWorker 专门负责单个流的长效维护
func (e *Engine) streamWorker(ns, pod, container string) {
	ls, err := e.streams.GetOrCreate(ns, pod, container, e.cfg)
	if err != nil {
		return
	}

	// 具体的读取循环交给 Engine 逻辑，但流的所有权在 StreamManager
	e.processStream(ls)
}

// processStream 私有处理逻辑
func (e *Engine) processStream(ls *LogStream) {
	scanner := ls.Scanner
	printer := icolor.NewPrinter()

	for scanner.Scan() {
		select {
		case <-e.ctx.Done():
			return
		default:
			line := scanner.Text()
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
}
