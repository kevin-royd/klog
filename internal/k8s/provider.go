package k8s

import (
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	providerInstance *ClientsetProvider
	once             sync.Once
)

// ClientsetProvider 管理全局唯一的 K8s 客户端底座
type ClientsetProvider struct {
	Clientset kubernetes.Interface
	Mu        sync.Mutex
}

// GetProvider 获取或初始化全局唯一的客户端工厂
func GetProvider(kubeconfig string) (*ClientsetProvider, error) {
	var err error
	once.Do(func() {
		config, cErr := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if cErr != nil {
			err = cErr
			return
		}

		// 关键点：限制最大空闲连接，防止由于 goroutine 复用导致的泄露
		config.Burst = 100
		config.QPS = 50

		cs, cErr := kubernetes.NewForConfig(config)
		if cErr != nil {
			err = cErr
			return
		}

		providerInstance = &ClientsetProvider{
			Clientset: cs,
		}
	})

	if err != nil {
		return nil, err
	}
	return providerInstance, nil
}
