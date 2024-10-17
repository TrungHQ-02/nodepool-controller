package controller

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// checkPodNodeSelector checks for the 'provision-for-team' NodeSelector in the Pod spec
func (r *PodReconciler) checkPodNodeSelector(ctx context.Context, pod *corev1.Pod) (string, bool) {
	logger := log.FromContext(ctx)

	nodeSelector := pod.Spec.NodeSelector
	provisionForTeamValue, exists := nodeSelector["provision-for-team"]
	if exists {
		logger.Info("Found NodeSelector 'provision-for-team'", "value", provisionForTeamValue)
	} else {
		logger.Info("'provision-for-team' key not found in NodeSelector")
	}

	return provisionForTeamValue, exists
}
