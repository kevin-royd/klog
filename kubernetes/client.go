package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client K8s客户端封装
type Client struct {
	Clientset kubernetes.Interface
	Config    *rest.Config
	Namespace string
}

// NewClient 创建K8s客户端
func NewClient(kubeconfig, namespace string) (*Client, error) {
	config, err := buildConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("构建K8s配置失败: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("创建K8s客户端失败: %w", err)
	}

	// 如果未指定namespace，获取当前context的namespace
	if namespace == "" {
		namespace = getCurrentNamespace(kubeconfig)
	}

	return &Client{
		Clientset: clientset,
		Config:    config,
		Namespace: namespace,
	}, nil
}

// buildConfig 构建K8s配置
func buildConfig(kubeconfig string) (*rest.Config, error) {
	// 优先使用传入的kubeconfig
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	// 环境变量
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return clientcmd.BuildConfigFromFlags("", env)
	}

	// 默认路径
	home, err := os.UserHomeDir()
	if err == nil {
		defaultPath := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultPath); err == nil {
			return clientcmd.BuildConfigFromFlags("", defaultPath)
		}
	}

	// InCluster 模式
	return rest.InClusterConfig()
}

// getCurrentNamespace 获取当前命名空间
func getCurrentNamespace(kubeconfig string) string {
	if kubeconfig == "" {
		if env := os.Getenv("KUBECONFIG"); env != "" {
			kubeconfig = env
		} else {
			home, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	config, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return "default"
	}

	ctx, ok := config.Contexts[config.CurrentContext]
	if !ok || ctx.Namespace == "" {
		return "default"
	}
	return ctx.Namespace
}

// GetAllNamespaces 获取所有命名空间
func (c *Client) GetAllNamespaces(ctx context.Context) ([]string, error) {
	nsList, err := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取命名空间列表失败: %w", err)
	}

	var namespaces []string
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.Name)
	}
	return namespaces, nil
}

// GetPodsByNamespace 按命名空间获取Pod列表
func (c *Client) GetPodsByNamespace(ctx context.Context, namespace string) ([]PodInfo, error) {
	pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Pod列表失败 [%s]: %w", namespace, err)
	}

	var podInfos []PodInfo
	for _, pod := range pods.Items {
		info := PodInfo{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Status:    string(pod.Status.Phase),
			NodeName:  pod.Spec.NodeName,
			Labels:    pod.Labels,
		}

		for _, c := range pod.Spec.Containers {
			info.Containers = append(info.Containers, c.Name)
		}

		// 检查是否有CrashLoopBackOff
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				info.CrashLoopBackOff = true
				info.RestartCount = int(cs.RestartCount)
			}
		}

		podInfos = append(podInfos, info)
	}
	return podInfos, nil
}

// SearchPods 跨namespace搜索Pod
func (c *Client) SearchPods(ctx context.Context, keyword string, namespaces []string) ([]PodInfo, error) {
	var allPods []PodInfo

	for _, ns := range namespaces {
		pods, err := c.GetPodsByNamespace(ctx, ns)
		if err != nil {
			continue // 跳过有权限问题的namespace
		}

		for _, pod := range pods {
			if matchPod(pod, keyword) {
				allPods = append(allPods, pod)
			}
		}
	}

	return allPods, nil
}

// matchPod 匹配Pod
func matchPod(pod PodInfo, keyword string) bool {
	keyword = strings.ToLower(keyword)

	// 名称匹配
	if strings.Contains(strings.ToLower(pod.Name), keyword) {
		return true
	}

	// 标签匹配
	for k, v := range pod.Labels {
		if strings.Contains(strings.ToLower(k), keyword) ||
			strings.Contains(strings.ToLower(v), keyword) {
			return true
		}
	}

	return false
}

// PodInfo Pod信息
type PodInfo struct {
	Name             string
	Namespace        string
	Status           string
	NodeName         string
	Containers       []string
	Labels           map[string]string
	CrashLoopBackOff bool
	RestartCount     int
}
