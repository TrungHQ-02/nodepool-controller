package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	//	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	DynamicClient dynamic.Interface
}

func NewPodReconciler(mgr ctrl.Manager) (*PodReconciler, error) {
	dynamicClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &PodReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		DynamicClient: dynamicClient,
	}, nil
}

// Reconcile is part of the main Kubernetes reconciliation loop
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Pod instance
	pod := &corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.Info("Pod not found. Ignoring since object must be deleted.", "pod", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod", "pod", req.NamespacedName)
		return ctrl.Result{}, err
	}

	// Check if the pod is pending
	if pod.Status.Phase == corev1.PodPending {
		logger.Info("Pod is pending", "pod", req.NamespacedName)

		provisionForTeamValue, exists := r.checkPodNodeSelector(ctx, pod)
		if !exists {
			return ctrl.Result{}, nil
		}

		matchingNodePoolFound, err := r.findMatchingNodePool(ctx, provisionForTeamValue)
		if err != nil {
			return ctrl.Result{}, err
		}

		if !matchingNodePoolFound {
			err := r.createNodePool(ctx, provisionForTeamValue)
			if err != nil {
				return ctrl.Result{}, err
			}
			// Optionally, you might want to requeue immediately to verify the NodePool creation
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
