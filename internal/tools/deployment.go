package tools

import corev1 "k8s.io/api/core/v1"

// GetDeploymentName extracts the deployment name from the pod's owner references
func GetDeploymentName(pod *corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			return ref.Name
		}
	}
	return ""
}
