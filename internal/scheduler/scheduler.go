package scheduler

import (
	"context"
	"fmt"
	"sync"

	"github.com/IRONICBo/distribute-scheduler/internal/config"
	"github.com/IRONICBo/distribute-scheduler/internal/tools"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// WorkloadCache holds the information about on-demand and spot pods for a specific namespace and deployment
// or statefulset(TODO). It also holds the maximum number of on-demand pods that can be created.
type WorkloadCache struct {
	workloadCacheMap map[string]*WorkloadInfo
	// TODO: add a shared lock for the cache
	mu sync.Mutex
}

type WorkloadInfo struct {
	// We do not save the pod object in the cache, only the count here
	onDemandCount    int
	spotCount        int
	maxOnDemandCount int
	replicas         int
	enabled          bool
}

// Scheduler struct holds the necessary clients and caches
type Scheduler struct {
	kubeClient    *kubernetes.Clientset
	workloadCache WorkloadCache
	informer      cache.SharedIndexInformer
}

// NewScheduler creates a new instance of the Scheduler
func NewScheduler() (*Scheduler, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Create a new Scheduler instance
	scheduler := &Scheduler{
		kubeClient: kubeClient,
		workloadCache: WorkloadCache{
			workloadCacheMap: make(map[string]*WorkloadInfo),
		},
		// Use simple lister/watcher for pods,
		// will be replaced with a more efficient one in the next step
		informer: cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().Pods("").List(context.TODO(), options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().Pods("").Watch(context.TODO(), options)
				},
			},
			&corev1.Pod{},
			0,
			cache.Indexers{},
		),
	}

	// Set up event handlers for the informer, now it is unused
	scheduler.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    scheduler.handlePodAdd,
		DeleteFunc: scheduler.handlePodDelete,
	})

	return scheduler, nil
}

// Run starts the scheduler and the informer
func (s *Scheduler) Run(stopCh <-chan struct{}) {
	// Start the informer
	go s.informer.Run(stopCh)

	// Wait for the informer cache to sync
	if !cache.WaitForCacheSync(stopCh, s.informer.HasSynced) {
		klog.V(0).ErrorS(nil, "Failed to sync cache")
		return
	}

	<-stopCh
}

// AddWorkloadCache adds a new WorkloadCache to the cache map
// TODO: add delete method for this cache
func (s *Scheduler) AddWorkloadCache(namespace, name string, maxOnDemandCount, replicas int, enable bool) {
	key := fmt.Sprintf("%s/%s", namespace, name)
	s.workloadCache.mu.Lock()
	defer s.workloadCache.mu.Unlock()

	s.workloadCache.workloadCacheMap[key] = &WorkloadInfo{
		onDemandCount:    0,
		maxOnDemandCount: maxOnDemandCount,
		replicas:         replicas,
		enabled:          enable,
	}

	klog.V(1).InfoS("WorkloadCache added", "key", key, "maxOnDemandCount", maxOnDemandCount, "replicas", replicas, "enable", enable)
}

// handlePodAdd is called when a new pod is added
func (s *Scheduler) handlePodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.V(0).ErrorS(nil, "Failed to convert object to pod in handlePodAdd")
		return
	}

	// Check if the pod has the relevant labels
	if nodeType, ok := pod.Labels[config.CapacityLabel]; ok {
		namespace := pod.Namespace
		deploymentName := tools.GetDeploymentName(pod)
		// caddy-deployment-6477dfc6c6 => caddy-deployment
		deploymentName = deploymentName[:len(deploymentName)-11]

		// Create a unique key for the namespace and deployment
		key := fmt.Sprintf("%s/%s", namespace, deploymentName)
		klog.V(1).InfoS("Pod added", "key", key, "pod", pod.Name)

		s.addToWorkloadCache(key, pod.Name, nodeType)
	}
}

// AddOnDemandPod adds an on-demand pod to the cache
func (s *Scheduler) AddOnDemandPod(namespace, deploymentName, podName string) {
	// Filter out the deployment name from the pod's owner references
	// caddy-deployment-6477dfc6c6 => caddy-deployment
	deploymentName = deploymentName[:len(deploymentName)-11]

	key := fmt.Sprintf("%s/%s", namespace, deploymentName)
	s.addToWorkloadCache(key, podName, config.OnDemandNodeType)
}

// AddSpotPod adds a spot pod to the cache
func (s *Scheduler) AddSpotPod(namespace, deploymentName, podName string) {
	// Filter out the deployment name from the pod's owner references
	// caddy-deployment-6477dfc6c6 => caddy-deployment
	deploymentName = deploymentName[:len(deploymentName)-11]

	key := fmt.Sprintf("%s/%s", namespace, deploymentName)
	s.addToWorkloadCache(key, podName, config.SpotNodeType)
}

// handlePodDelete is called when a pod is deleted
// TODO: delete might not be accurate, need to check if the pod is evicted or deleted
func (s *Scheduler) handlePodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.V(0).ErrorS(nil, "Failed to convert object to pod in handlePodDelete")
		return
	}

	// Check if the pod has the relevant labels
	if nodeType, ok := pod.Labels[config.CapacityLabel]; ok {
		namespace := pod.Namespace
		deploymentName := tools.GetDeploymentName(pod)

		// Create a unique key for the namespace and deployment
		key := fmt.Sprintf("%s/%s", namespace, deploymentName)

		// Remove the pod from the cache
		s.removeFromWorkloadCache(key, pod.Name, nodeType)
	}
}

// addToWorkloadCache adds a pod to the appropriate cache list (on-demand or spot)
func (s *Scheduler) addToWorkloadCache(key, podName, podType string) {
	s.workloadCache.mu.Lock()
	defer s.workloadCache.mu.Unlock()

	workloadInfo, ok := s.workloadCache.workloadCacheMap[key]
	if !ok {
		klog.V(1).ErrorS(nil, "Current key not found in workloadCacheMap", "key", key, "pod", podName, "type", podType)
		return
	}

	if podType == config.OnDemandNodeType {
		workloadInfo.onDemandCount++
	} else {
		workloadInfo.spotCount++
	}
	s.workloadCache.workloadCacheMap[key] = workloadInfo
	klog.V(1).InfoS("Pod added to cache", "key", key, "pod", podName, "type", podType)
}

// removeFromWorkloadCache removes a pod from the cache
func (s *Scheduler) removeFromWorkloadCache(key, podName string, podType string) {
	s.workloadCache.mu.Lock()
	defer s.workloadCache.mu.Unlock()

	// Delete the pod from the cache
	workloadInfo, ok := s.workloadCache.workloadCacheMap[key]
	if !ok {
		klog.V(1).ErrorS(nil, "Current key not found in workloadCacheMap", "key", key, "pod", podName, "type", podType)
		return
	}

	if podType == config.OnDemandNodeType {
		workloadInfo.onDemandCount--
	} else {
		workloadInfo.spotCount--
	}

	s.workloadCache.workloadCacheMap[key] = workloadInfo
	klog.V(1).InfoS("Pod removed from cache", "key", key, "pod", podName, "type", podType)
}

// ShouldLimitOnDemandPods checks if the on-demand pod count exceeds the configured limit
func (s *Scheduler) ShouldLimitOnDemandPods(pod *corev1.Pod) bool {
	s.workloadCache.mu.Lock()
	defer s.workloadCache.mu.Unlock()

	deploymentName := tools.GetDeploymentName(pod)
	// caddy-deployment-6477dfc6c6 => caddy-deployment
	deploymentName = deploymentName[:len(deploymentName)-11]
	key := fmt.Sprintf("%s/%s", pod.Namespace, deploymentName)
	workload, ok := s.workloadCache.workloadCacheMap[key]
	if !ok {
		klog.V(1).ErrorS(nil, "Current key not found in workloadCacheMap", "key", key)
		return false
	}

	return workload.onDemandCount < workload.maxOnDemandCount
}
