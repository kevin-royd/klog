package kubernetes

import (
	"context"
	"fmt"
	"sync"
	"time"

	"klog/internal/color"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// PodEvent Pod事件
type PodEvent struct {
	Type      watch.EventType
	Pod       PodInfo
	Message   string
	Timestamp time.Time
}

// PodWatcher Pod监视器
type PodWatcher struct {
	client    *Client
	printer   *color.Printer
	mu        sync.Mutex
	watchers  []watch.Interface
	eventChan chan PodEvent
	stopChan  chan struct{}
}

// NewPodWatcher 创建Pod监视器
func NewPodWatcher(client *Client, printer *color.Printer) *PodWatcher {
	return &PodWatcher{
		client:    client,
		printer:   printer,
		eventChan: make(chan PodEvent, 100),
		stopChan:  make(chan struct{}),
	}
}

// Watch 开始监视指定namespace的Pod变化
func (w *PodWatcher) Watch(ctx context.Context, namespace string, labelSelector string) error {
	opts := metav1.ListOptions{
		LabelSelector: labelSelector,
	}

	watcher, err := w.client.Clientset.CoreV1().Pods(namespace).Watch(ctx, opts)
	if err != nil {
		return fmt.Errorf("创建Pod Watcher失败 [%s]: %w", namespace, err)
	}

	w.mu.Lock()
	w.watchers = append(w.watchers, watcher)
	w.mu.Unlock()

	go func() {
		defer watcher.Stop()
		for {
			select {
			case event, ok := <-watcher.ResultChan():
				if !ok {
					return
				}
				pod, ok := event.Object.(*corev1.Pod)
				if !ok {
					continue
				}

				podInfo := PodInfo{
					Name:      pod.Name,
					Namespace: pod.Namespace,
					Status:    string(pod.Status.Phase),
					NodeName:  pod.Spec.NodeName,
					Labels:    pod.Labels,
				}
				for _, c := range pod.Spec.Containers {
					podInfo.Containers = append(podInfo.Containers, c.Name)
				}

				msg := w.buildEventMessage(event.Type, pod)

				w.eventChan <- PodEvent{
					Type:      event.Type,
					Pod:       podInfo,
					Message:   msg,
					Timestamp: time.Now(),
				}

			case <-ctx.Done():
				return
			case <-w.stopChan:
				return
			}
		}
	}()

	return nil
}

// WatchMultiNamespace 跨namespace监视
func (w *PodWatcher) WatchMultiNamespace(ctx context.Context, namespaces []string, labelSelector string) error {
	for _, ns := range namespaces {
		if err := w.Watch(ctx, ns, labelSelector); err != nil {
			color.Warn("无法监视namespace '%s': %v", ns, err)
			continue
		}
	}
	return nil
}

// Events 获取事件channel
func (w *PodWatcher) Events() <-chan PodEvent {
	return w.eventChan
}

// Stop 停止所有监视
func (w *PodWatcher) Stop() {
	close(w.stopChan)

	w.mu.Lock()
	defer w.mu.Unlock()

	for _, watcher := range w.watchers {
		watcher.Stop()
	}
	w.watchers = nil
}

// PrintEvent 打印Pod事件
func (w *PodWatcher) PrintEvent(event PodEvent) {
	timestamp := event.Timestamp.Format("15:04:05")

	switch event.Type {
	case watch.Added:
		color.Success("[%s] Pod 创建: %s/%s (%s)",
			timestamp, event.Pod.Namespace, event.Pod.Name, event.Pod.Status)
	case watch.Modified:
		// 检查CrashLoopBackOff
		if event.Pod.CrashLoopBackOff {
			color.Error("[%s] ⚠️  CrashLoopBackOff: %s/%s (重启 %d 次)",
				timestamp, event.Pod.Namespace, event.Pod.Name, event.Pod.RestartCount)
		} else {
			color.Info("[%s] Pod 更新: %s/%s → %s",
				timestamp, event.Pod.Namespace, event.Pod.Name, event.Pod.Status)
		}
	case watch.Deleted:
		color.Warn("[%s] Pod 删除: %s/%s",
			timestamp, event.Pod.Namespace, event.Pod.Name)
	case watch.Error:
		color.Error("[%s] Pod 错误: %s/%s - %s",
			timestamp, event.Pod.Namespace, event.Pod.Name, event.Message)
	}

	if event.Message != "" {
		fmt.Printf("  └─ %s\n", event.Message)
	}
}

// buildEventMessage 构建事件消息
func (w *PodWatcher) buildEventMessage(eventType watch.EventType, pod *corev1.Pod) string {
	var messages []string

	// 容器状态详情
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			messages = append(messages,
				fmt.Sprintf("容器 %s 等待中: %s (%s)",
					cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
		}
		if cs.State.Terminated != nil {
			messages = append(messages,
				fmt.Sprintf("容器 %s 已终止: %s (退出码: %d)",
					cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode))
		}
		if cs.RestartCount > 0 {
			messages = append(messages,
				fmt.Sprintf("容器 %s 重启次数: %d", cs.Name, cs.RestartCount))
		}
	}

	// Pod条件
	for _, cond := range pod.Status.Conditions {
		if cond.Status == corev1.ConditionFalse && cond.Message != "" {
			messages = append(messages,
				fmt.Sprintf("条件 %s: %s", cond.Type, cond.Message))
		}
	}

	if len(messages) > 0 {
		result := ""
		for i, msg := range messages {
			if i > 0 {
				result += " | "
			}
			result += msg
		}
		return result
	}
	return ""
}
