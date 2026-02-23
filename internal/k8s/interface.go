package k8s

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K8sClient 标准 K8s 行为接口 (可 Mock)
type K8sClient interface {
	// 获取底层 Clientset
	GetClientset() kubernetes.Interface

	// Pod 相关
	ListPods(ctx context.Context, ns string, opts metav1.ListOptions) ([]*corev1.Pod, error)
	GetPod(ctx context.Context, ns, name string) (*corev1.Pod, error)

	// Namespace 相关
	GetAllNamespaces(ctx context.Context) ([]string, error)
	GetCurrentNamespace() string
}

// 确保 Client 实现了接口
var _ K8sClient = (*Client)(nil)

// GetClientset 实现
func (c *Client) GetClientset() kubernetes.Interface {
	return c.Clientset
}

// GetCurrentNamespace 实现
func (c *Client) GetCurrentNamespace() string {
	return c.Namespace
}
