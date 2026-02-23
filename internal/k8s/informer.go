package k8s

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// PodInformer 事件定义
type PodInformerEvent struct {
	Type string
	Pod  *corev1.Pod
}

// PodInformer 具备生命周期管理的 Informer 包装
type PodInformer struct {
	factory      informers.SharedInformerFactory
	eventChan    chan PodInformerEvent
	status       int32 // 0: new, 1: running, 2: stopped
	mu           sync.Mutex
	stopChan     chan struct{}
	namespace    string
	resyncPeriod time.Duration
}

func NewPodInformer(kubeconfig, namespace string) *PodInformer {
	client, err := NewClient(kubeconfig, namespace)
	if err != nil {
		return nil
	}

	return &PodInformer{
		factory:      informers.NewSharedInformerFactoryWithOptions(client.Clientset, 0, informers.WithNamespace(namespace)),
		eventChan:    make(chan PodInformerEvent, 1000),
		namespace:    namespace,
		resyncPeriod: 30 * time.Second,
	}
}

// Start 幂等启动
func (i *PodInformer) Start(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !atomic.CompareAndSwapInt32(&i.status, 0, 1) && !atomic.CompareAndSwapInt32(&i.status, 2, 1) {
		return nil // 已经在运行
	}

	i.stopChan = make(chan struct{})
	podInformer := i.factory.Core().V1().Pods().Informer()

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if pod, ok := obj.(*corev1.Pod); ok {
				i.eventChan <- PodInformerEvent{Type: "added", Pod: pod}
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			//TODO: 可以在这里处理 Pod 状态变更，如 Pending -> Running
		},
		DeleteFunc: func(obj interface{}) {
			if pod, ok := obj.(*corev1.Pod); ok {
				i.eventChan <- PodInformerEvent{Type: "deleted", Pod: pod}
			}
		},
	})

	go i.factory.Start(i.stopChan)

	// 等待初始同步
	if !cache.WaitForCacheSync(i.stopChan, podInformer.HasSynced) {
		return context.DeadlineExceeded
	}

	return nil
}

// Stop 彻底关停
func (i *PodInformer) Stop() {
	i.mu.Lock()
	defer i.mu.Unlock()

	if atomic.CompareAndSwapInt32(&i.status, 1, 2) {
		close(i.stopChan)
	}
}

func (i *PodInformer) Events() <-chan PodInformerEvent {
	return i.eventChan
}
