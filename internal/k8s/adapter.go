package k8s

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// Adapter Kubernetes 适配器接口 (工业级解耦)
type Adapter interface {
	// 核心操作
	GetNamespace() string
	Clientset() kubernetes.Interface

	// 资源发现与解析
	GetAllNamespaces(ctx context.Context) ([]string, error)
	ResolvePods(ctx context.Context, resource string) ([]*corev1.Pod, error)
	GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error)

	// 事件与监控
	WatchPods(ctx context.Context, namespace string, labelSelector string) (<-chan PodEvent, error)
}

// PodEvent 封装后的 Pod 事件
type PodEvent struct {
	Type string
	Pod  *corev1.Pod
}

// --- 实现 ---

type k8sAdapter struct {
	client    *Client
	resolver  *Resolver
	namespace string
}

func NewAdapter(kubeconfig, ns string) (Adapter, error) {
	client, err := NewClient(kubeconfig, ns)
	if err != nil {
		return nil, err
	}

	return &k8sAdapter{
		client:    client,
		resolver:  NewResolver(client),
		namespace: client.Namespace,
	}, nil
}

func (a *k8sAdapter) GetAllNamespaces(ctx context.Context) ([]string, error) {
	return a.client.GetAllNamespaces(ctx)
}

func (a *k8sAdapter) GetNamespace() string { return a.namespace }

func (a *k8sAdapter) Clientset() kubernetes.Interface { return a.client.Clientset }

func (a *k8sAdapter) GetPod(ctx context.Context, ns, name string) (*corev1.Pod, error) {
	return a.client.GetPod(ctx, ns, name)
}

func (a *k8sAdapter) ResolvePods(ctx context.Context, resource string) ([]*corev1.Pod, error) {
	ref := a.resolver.ParseResource(resource)
	podInfos, err := a.resolver.ResolveToPods(ctx, ref, a.namespace)
	if err != nil {
		return nil, err
	}

	var res []*corev1.Pod
	for _, p := range podInfos {
		pod, err := a.client.GetPod(ctx, p.Namespace, p.Name)
		if err == nil {
			res = append(res, pod)
		}
	}
	return res, nil
}

func (a *k8sAdapter) WatchPods(ctx context.Context, ns string, selector string) (<-chan PodEvent, error) {
	return nil, nil // TODO
}
