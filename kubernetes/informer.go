package kubernetes

import (
	"context"
	"fmt"
	"sync"
	"time"

	"klog/internal/color"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// PodInformerEvent Informer事件类型
type PodInformerEvent struct {
	Type      string // "add", "update", "delete"
	Pod       *corev1.Pod
	OldPod    *corev1.Pod // update时的旧Pod
	Timestamp time.Time
}

// PodInformer 基于Informer的Pod监视器（比Watch更高效）
type PodInformer struct {
	client        *Client
	printer       *color.Printer
	factory       informers.SharedInformerFactory
	stopChan      chan struct{}
	eventChan     chan PodInformerEvent
	mu            sync.Mutex
	started       bool
	resyncPeriod  time.Duration
	namespace     string
	labelSelector string
	onCrashLoop   func(pod *corev1.Pod)
}

// NewPodInformer 创建基于Informer的Pod监视器
func NewPodInformer(client *Client, namespace string) *PodInformer {
	return &PodInformer{
		client:       client,
		printer:      color.NewPrinter(),
		stopChan:     make(chan struct{}),
		eventChan:    make(chan PodInformerEvent, 200),
		resyncPeriod: 30 * time.Second,
		namespace:    namespace,
	}
}

// SetResyncPeriod 设置重新同步周期
func (i *PodInformer) SetResyncPeriod(period time.Duration) {
	i.resyncPeriod = period
}

// SetLabelSelector 设置标签选择器
func (i *PodInformer) SetLabelSelector(selector string) {
	i.labelSelector = selector
}

// OnCrashLoop 设置CrashLoop回调
func (i *PodInformer) OnCrashLoop(fn func(pod *corev1.Pod)) {
	i.onCrashLoop = fn
}

// Start 启动Informer
func (i *PodInformer) Start(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.started {
		return fmt.Errorf("Informer已启动")
	}

	// 创建InformerFactory
	var opts []informers.SharedInformerOption
	if i.namespace != "" {
		opts = append(opts, informers.WithNamespace(i.namespace))
	}

	i.factory = informers.NewSharedInformerFactoryWithOptions(
		i.client.Clientset,
		i.resyncPeriod,
		opts...,
	)

	// 获取Pod Informer
	podInformer := i.factory.Core().V1().Pods().Informer()

	// 注册事件处理器
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			if i.matchSelector(pod) {
				i.eventChan <- PodInformerEvent{
					Type:      "add",
					Pod:       pod,
					Timestamp: time.Now(),
				}
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod, ok1 := oldObj.(*corev1.Pod)
			newPod, ok2 := newObj.(*corev1.Pod)
			if !ok1 || !ok2 {
				return
			}
			if i.matchSelector(newPod) {
				event := PodInformerEvent{
					Type:      "update",
					Pod:       newPod,
					OldPod:    oldPod,
					Timestamp: time.Now(),
				}
				i.eventChan <- event

				// 检测CrashLoopBackOff
				i.checkCrashLoop(newPod)
			}
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				// 处理DeletedFinalStateUnknown
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					return
				}
			}
			if i.matchSelector(pod) {
				i.eventChan <- PodInformerEvent{
					Type:      "delete",
					Pod:       pod,
					Timestamp: time.Now(),
				}
			}
		},
	})

	// 启动Informer
	i.factory.Start(i.stopChan)

	// 等待缓存同步
	color.Info("正在同步Informer缓存...")
	i.factory.WaitForCacheSync(i.stopChan)
	color.Success("Informer缓存同步完成")

	i.started = true

	// 监听context取消
	go func() {
		select {
		case <-ctx.Done():
			i.Stop()
		case <-i.stopChan:
		}
	}()

	return nil
}

// Stop 停止Informer
func (i *PodInformer) Stop() {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.started {
		return
	}

	close(i.stopChan)
	i.started = false
}

// Events 获取事件channel
func (i *PodInformer) Events() <-chan PodInformerEvent {
	return i.eventChan
}

// ListPods 获取当前缓存中的所有Pod
func (i *PodInformer) ListPods() ([]*corev1.Pod, error) {
	if !i.started {
		return nil, fmt.Errorf("Informer未启动")
	}

	lister := i.factory.Core().V1().Pods().Lister()
	pods, err := lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	if i.labelSelector == "" {
		return pods, nil
	}

	// 过滤
	var filtered []*corev1.Pod
	for _, pod := range pods {
		if i.matchSelector(pod) {
			filtered = append(filtered, pod)
		}
	}
	return filtered, nil
}

// GetPod 从缓存获取Pod
func (i *PodInformer) GetPod(namespace, name string) (*corev1.Pod, error) {
	if !i.started {
		return nil, fmt.Errorf("Informer未启动")
	}

	lister := i.factory.Core().V1().Pods().Lister()
	return lister.Pods(namespace).Get(name)
}

// matchSelector 检查Pod是否匹配标签选择器
func (i *PodInformer) matchSelector(pod *corev1.Pod) bool {
	if i.labelSelector == "" {
		return true
	}

	selector, err := labels.Parse(i.labelSelector)
	if err != nil {
		return true
	}
	return selector.Matches(labels.Set(pod.Labels))
}

// checkCrashLoop 检测CrashLoopBackOff
func (i *PodInformer) checkCrashLoop(pod *corev1.Pod) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			color.Error("🔴 CrashLoopBackOff 检测到: %s/%s (容器: %s, 重启: %d次)",
				pod.Namespace, pod.Name, cs.Name, cs.RestartCount)

			if cs.LastTerminationState.Terminated != nil {
				color.Warn("  └─ 上次终止原因: %s, 退出码: %d",
					cs.LastTerminationState.Terminated.Reason,
					cs.LastTerminationState.Terminated.ExitCode)
			}

			if i.onCrashLoop != nil {
				i.onCrashLoop(pod)
			}
		}
	}
}

// PrintEvent 打印Informer事件
func (i *PodInformer) PrintEvent(event PodInformerEvent) {
	timestamp := event.Timestamp.Format("15:04:05.000")

	switch event.Type {
	case "add":
		color.Success("[%s] ➕ Pod 新增: %s/%s (状态: %s, 节点: %s)",
			timestamp, event.Pod.Namespace, event.Pod.Name,
			event.Pod.Status.Phase, event.Pod.Spec.NodeName)

	case "update":
		oldPhase := ""
		if event.OldPod != nil {
			oldPhase = string(event.OldPod.Status.Phase)
		}
		newPhase := string(event.Pod.Status.Phase)

		if oldPhase != newPhase {
			color.Info("[%s] 🔄 Pod 状态变更: %s/%s (%s → %s)",
				timestamp, event.Pod.Namespace, event.Pod.Name, oldPhase, newPhase)
		}

		// 容器状态详情
		for _, cs := range event.Pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				color.Warn("[%s]   └─ 容器 %s: %s",
					timestamp, cs.Name, cs.State.Waiting.Reason)
			}
		}

	case "delete":
		color.Warn("[%s] ➖ Pod 删除: %s/%s",
			timestamp, event.Pod.Namespace, event.Pod.Name)
	}
}
