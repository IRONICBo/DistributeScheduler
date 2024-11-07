package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/IRONICBo/distribute-scheduler/internal/config"
	"github.com/IRONICBo/distribute-scheduler/internal/scheduler"
	"github.com/IRONICBo/distribute-scheduler/internal/tools"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/klog/v2"
)

const (
	DeploymentLabel = "Deployment"
	PodLabel        = "Pod"
)

type WebhookHandler struct {
	scheduler *scheduler.Scheduler
	decoder   runtime.Decoder
}

func NewWebhookHandler(stopCh <-chan struct{}) *WebhookHandler {
	scheduler, err := scheduler.NewScheduler()
	if err != nil {
		klog.V(0).ErrorS(err, "failed to create scheduler")
		panic(err)
	}
	go scheduler.Run(stopCh)

	codecs := serializer.NewCodecFactory(runtime.NewScheme())
	decoder := codecs.UniversalDecoder()

	return &WebhookHandler{
		scheduler: scheduler,
		decoder:   decoder,
	}
}

// MutateHandler is the handler for the webhook server.
func (h *WebhookHandler) MutateHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var admissionReview admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &admissionReview); err != nil {
		http.Error(w, "could not parse request", http.StatusBadRequest)
		return
	}

	if admissionReview.Request == nil {
		http.Error(w, "request is empty", http.StatusBadRequest)
		return
	}

	if admissionReview.Request.Kind.Kind == PodLabel {
		klog.V(1).InfoS("Handling pod add")
		var obj corev1.Pod
		_, _, err := h.decoder.Decode(admissionReview.Request.Object.Raw, nil, &obj)
		if err != nil {
			klog.V(0).ErrorS(nil, "Failed to convert object to pod in handlePodAdd")
			http.Error(w, "could not convert object to pod", http.StatusInternalServerError)
			return
		}
		response := h.mutatePod(&obj)
		admissionReview.Response = response
		admissionReview.Response.UID = admissionReview.Request.UID
	} else if admissionReview.Request.Kind.Kind == DeploymentLabel {
		klog.V(1).InfoS("Handling deployment add")
		var obj appsv1.Deployment
		_, _, err := h.decoder.Decode(admissionReview.Request.Object.Raw, nil, &obj)
		if err != nil {
			klog.V(0).ErrorS(nil, "Failed to convert object to deployment in handleDeploymentAdd")
			http.Error(w, "could not convert object to deployment", http.StatusInternalServerError)
			return
		}
		// Init deployment cache
		h.handleDeploymentAdd(&obj)
	}

	if admissionReview.Response == nil {
		admissionReview.Response = &admissionv1.AdmissionResponse{
			UID:     admissionReview.Request.UID,
			Allowed: true,
		}
	}

	responseBytes, err := json.Marshal(admissionReview)
	if err != nil {
		http.Error(w, "could not marshal response", http.StatusInternalServerError)
		return
	}

	_, err = w.Write(responseBytes)
	if err != nil {
		klog.V(0).ErrorS(err, "could not write response")
		http.Error(w, "could not write response", http.StatusInternalServerError)
		return
	}
}

func (h *WebhookHandler) handleDeploymentAdd(deployment *appsv1.Deployment) {
	namespace := deployment.Namespace
	deploymentName := deployment.Name
	replicas := int(*deployment.Spec.Replicas)

	var maxOnDemandCount int
	var enable bool
	labels := deployment.ObjectMeta.Labels
	if labels[config.WebhookSchedulerLabel] == "true" {
		enable = true
	} else {
		enable = false
	}

	// Default to replicas if maxOnDemandCount is not set
	if labels[config.WebhookSchedulerMaxOnDemandCount] != "" {
		count, err := strconv.Atoi(labels[config.WebhookSchedulerMaxOnDemandCount])
		if err != nil {
			klog.V(1).ErrorS(err, "Failed to convert maxOnDemandCount to int")
			maxOnDemandCount = replicas
		} else {
			maxOnDemandCount = count
		}
	} else {
		maxOnDemandCount = replicas
	}

	h.scheduler.AddWorkloadCache(namespace, deploymentName, maxOnDemandCount, replicas, enable)
}

func (h *WebhookHandler) mutatePod(pod *corev1.Pod) *admissionv1.AdmissionResponse {
	patch, err := h.createAffinityPatch(pod)
	if err != nil {
		klog.V(0).ErrorS(err, "Error creating affinity patch")
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Patch:   patch,
		PatchType: func() *admissionv1.PatchType {
			pt := admissionv1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

func (h *WebhookHandler) createAffinityPatch(pod *corev1.Pod) ([]byte, error) {
	affinity := corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      config.CapacityLabel,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{},
							},
						},
					},
				},
			},
		},
	}

	if h.scheduler.ShouldLimitOnDemandPods(pod) {
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values = append(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values, "on-demand")
		h.scheduler.AddOnDemandPod(pod.Namespace, tools.GetDeploymentName(pod), pod.Name)
	} else {
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values = append(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values, "spot")
		h.scheduler.AddSpotPod(pod.Namespace, tools.GetDeploymentName(pod), pod.Name)
	}

	// TODO: Add delete cost for pod in annotation

	patch := []map[string]interface{}{
		{
			"op":    "add",
			"path":  "/spec/affinity",
			"value": affinity,
		},
	}

	return json.Marshal(patch)
}
