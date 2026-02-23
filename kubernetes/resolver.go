package kubernetes

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ResourceType 资源类型
type ResourceType string

const (
	ResourcePod         ResourceType = "pod"
	ResourceDeployment  ResourceType = "deployment"
	ResourceStatefulSet ResourceType = "statefulset"
	ResourceDaemonSet   ResourceType = "daemonset"
	ResourceReplicaSet  ResourceType = "replicaset"
	ResourceJob         ResourceType = "job"
	ResourceCronJob     ResourceType = "cronjob"
	ResourceService     ResourceType = "service"
)

// ResourceRef 资源引用
type ResourceRef struct {
	Type      ResourceType
	Name      string
	Namespace string
}

// Resolver 资源解析器 - 将各种K8s资源解析为Pod列表
type Resolver struct {
	client *Client
}

// NewResolver 创建资源解析器
func NewResolver(client *Client) *Resolver {
	return &Resolver{client: client}
}

// ParseResource 解析资源类型和名称
// 支持格式: deployment/nginx, deploy/nginx, svc/myservice, pod/mypod, nginx (自动推断)
func (r *Resolver) ParseResource(resource string) ResourceRef {
	parts := strings.SplitN(resource, "/", 2)

	if len(parts) == 2 {
		resType := normalizeResourceType(parts[0])
		return ResourceRef{
			Type: resType,
			Name: parts[1],
		}
	}

	// 只有名称，自动推断
	return ResourceRef{
		Type: "", // 空表示自动推断
		Name: resource,
	}
}

// ResolveToPods 将资源解析为Pod列表
func (r *Resolver) ResolveToPods(ctx context.Context, ref ResourceRef, namespace string) ([]PodInfo, error) {
	if namespace == "" {
		namespace = r.client.Namespace
	}
	ref.Namespace = namespace

	switch ref.Type {
	case ResourcePod:
		return r.resolvePod(ctx, ref)
	case ResourceDeployment:
		return r.resolveDeployment(ctx, ref)
	case ResourceStatefulSet:
		return r.resolveStatefulSet(ctx, ref)
	case ResourceDaemonSet:
		return r.resolveDaemonSet(ctx, ref)
	case ResourceReplicaSet:
		return r.resolveReplicaSet(ctx, ref)
	case ResourceJob:
		return r.resolveJob(ctx, ref)
	case ResourceCronJob:
		return r.resolveCronJob(ctx, ref)
	case ResourceService:
		return r.resolveService(ctx, ref)
	case "":
		return r.autoResolve(ctx, ref)
	default:
		return nil, fmt.Errorf("不支持的资源类型: %s", ref.Type)
	}
}

// autoResolve 自动推断资源类型并解析
func (r *Resolver) autoResolve(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	namespace := ref.Namespace

	// 1. 先尝试 Pod
	pods, err := r.client.GetPodsByNamespace(ctx, namespace)
	if err == nil {
		for _, p := range pods {
			if strings.Contains(p.Name, ref.Name) {
				ref.Type = ResourcePod
				return r.resolveByNameMatch(pods, ref.Name), nil
			}
		}
	}

	// 2. 尝试 Deployment
	deploys, err := r.client.Clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, d := range deploys.Items {
			if d.Name == ref.Name || strings.Contains(d.Name, ref.Name) {
				ref.Type = ResourceDeployment
				ref.Name = d.Name
				return r.resolveDeployment(ctx, ref)
			}
		}
	}

	// 3. 尝试 StatefulSet
	ss, err := r.client.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, s := range ss.Items {
			if s.Name == ref.Name || strings.Contains(s.Name, ref.Name) {
				ref.Type = ResourceStatefulSet
				ref.Name = s.Name
				return r.resolveStatefulSet(ctx, ref)
			}
		}
	}

	// 4. 尝试 DaemonSet
	ds, err := r.client.Clientset.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, d := range ds.Items {
			if d.Name == ref.Name || strings.Contains(d.Name, ref.Name) {
				ref.Type = ResourceDaemonSet
				ref.Name = d.Name
				return r.resolveDaemonSet(ctx, ref)
			}
		}
	}

	// 5. 尝试 Service
	ref.Type = ResourceService
	svcPods, err := r.resolveService(ctx, ref)
	if err == nil && len(svcPods) > 0 {
		return svcPods, nil
	}

	return nil, fmt.Errorf("无法解析资源 '%s'，在namespace '%s' 中未找到匹配", ref.Name, namespace)
}

// resolveByNameMatch 按名称模糊匹配
func (r *Resolver) resolveByNameMatch(pods []PodInfo, name string) []PodInfo {
	var matched []PodInfo
	for _, p := range pods {
		if strings.Contains(p.Name, name) {
			matched = append(matched, p)
		}
	}
	return matched
}

// resolvePod 直接返回Pod
func (r *Resolver) resolvePod(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	pods, err := r.client.GetPodsByNamespace(ctx, ref.Namespace)
	if err != nil {
		return nil, err
	}

	var matched []PodInfo
	for _, p := range pods {
		if p.Name == ref.Name || strings.Contains(p.Name, ref.Name) {
			matched = append(matched, p)
		}
	}

	if len(matched) == 0 {
		return nil, fmt.Errorf("Pod '%s' 不存在于 namespace '%s'", ref.Name, ref.Namespace)
	}
	return matched, nil
}

// resolveDeployment Deployment → ReplicaSet → Pod
func (r *Resolver) resolveDeployment(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	deploy, err := r.client.Clientset.AppsV1().Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Deployment '%s' 不存在: %w", ref.Name, err)
	}

	return r.resolveBySelector(ctx, ref.Namespace, deploy.Spec.Selector)
}

// resolveStatefulSet StatefulSet → Pod
func (r *Resolver) resolveStatefulSet(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	ss, err := r.client.Clientset.AppsV1().StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("StatefulSet '%s' 不存在: %w", ref.Name, err)
	}

	return r.resolveBySelector(ctx, ref.Namespace, ss.Spec.Selector)
}

// resolveDaemonSet DaemonSet → Pod
func (r *Resolver) resolveDaemonSet(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	ds, err := r.client.Clientset.AppsV1().DaemonSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("DaemonSet '%s' 不存在: %w", ref.Name, err)
	}

	return r.resolveBySelector(ctx, ref.Namespace, ds.Spec.Selector)
}

// resolveReplicaSet ReplicaSet → Pod
func (r *Resolver) resolveReplicaSet(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	rs, err := r.client.Clientset.AppsV1().ReplicaSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("ReplicaSet '%s' 不存在: %w", ref.Name, err)
	}

	return r.resolveBySelector(ctx, ref.Namespace, rs.Spec.Selector)
}

// resolveJob Job → Pod
func (r *Resolver) resolveJob(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	job, err := r.client.Clientset.BatchV1().Jobs(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Job '%s' 不存在: %w", ref.Name, err)
	}

	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"job-name": job.Name,
		},
	}
	return r.resolveBySelector(ctx, ref.Namespace, selector)
}

// resolveCronJob CronJob → Job → Pod
func (r *Resolver) resolveCronJob(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	cronJob, err := r.client.Clientset.BatchV1().CronJobs(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("CronJob '%s' 不存在: %w", ref.Name, err)
	}

	// 获取CronJob创建的所有Job
	jobList, err := r.client.Clientset.BatchV1().Jobs(ref.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var allPods []PodInfo
	for _, job := range jobList.Items {
		for _, ownerRef := range job.OwnerReferences {
			if ownerRef.Name == cronJob.Name {
				jobRef := ResourceRef{Type: ResourceJob, Name: job.Name, Namespace: ref.Namespace}
				pods, err := r.resolveJob(ctx, jobRef)
				if err == nil {
					allPods = append(allPods, pods...)
				}
			}
		}
	}
	return allPods, nil
}

// resolveService Service → Endpoints → Pod
func (r *Resolver) resolveService(ctx context.Context, ref ResourceRef) ([]PodInfo, error) {
	svc, err := r.client.Clientset.CoreV1().Services(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Service '%s' 不存在: %w", ref.Name, err)
	}

	if len(svc.Spec.Selector) == 0 {
		return nil, fmt.Errorf("Service '%s' 没有selector", ref.Name)
	}

	selector := &metav1.LabelSelector{
		MatchLabels: svc.Spec.Selector,
	}
	return r.resolveBySelector(ctx, ref.Namespace, selector)
}

// resolveBySelector 通过标签选择器获取Pod
func (r *Resolver) resolveBySelector(ctx context.Context, namespace string, selector *metav1.LabelSelector) ([]PodInfo, error) {
	labelSelector, err := convertSelector(selector)
	if err != nil {
		return nil, err
	}

	pods, err := r.client.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
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

// convertSelector 转换标签选择器
func convertSelector(selector *metav1.LabelSelector) (string, error) {
	if selector == nil {
		return "", nil
	}

	ls, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return "", err
	}
	return ls.String(), nil
}

// normalizeResourceType 标准化资源类型
func normalizeResourceType(s string) ResourceType {
	switch strings.ToLower(s) {
	case "pod", "po", "pods":
		return ResourcePod
	case "deployment", "deploy", "deployments":
		return ResourceDeployment
	case "statefulset", "sts", "statefulsets":
		return ResourceStatefulSet
	case "daemonset", "ds", "daemonsets":
		return ResourceDaemonSet
	case "replicaset", "rs", "replicasets":
		return ResourceReplicaSet
	case "job", "jobs":
		return ResourceJob
	case "cronjob", "cj", "cronjobs":
		return ResourceCronJob
	case "service", "svc", "services":
		return ResourceService
	default:
		return ResourceType(strings.ToLower(s))
	}
}

// 确保使用了导入的包
var _ = labels.Everything
var _ *appsv1.Deployment
