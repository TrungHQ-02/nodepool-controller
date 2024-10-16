// controller/pod_controller.go

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

// NewPodReconciler initializes a new PodReconciler with a dynamic client
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

// listNodePools lists NodePool objects using the dynamic client
func (r *PodReconciler) listNodePools(ctx context.Context) ([]unstructured.Unstructured, error) {
	logger := log.FromContext(ctx)
	// Define the GVR for the NodePool CRD
	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Ensure DynamicClient is initialized
	if r.DynamicClient == nil {
		logger.Error(fmt.Errorf("dynamic client is nil"), "Dynamic client is not initialized")
		return nil, fmt.Errorf("dynamic client is nil")
	}

	// List NodePool objects across all namespaces (adjust if NodePool is namespace-scoped)
	nodePoolsList, err := r.DynamicClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list NodePools: %w", err)
	}

	nodePools := nodePoolsList.Items

	// // Log NodePools and their requirements
	// for _, nodePool := range nodePools {
	// 	logger.Info("NodePool found", "name", nodePool.GetName())
	//
	// 	// Extract the 'requirements' field
	// 	requirements, found, err := unstructured.NestedSlice(nodePool.Object, "spec", "template", "spec", "requirements")
	// 	if err != nil {
	// 		logger.Error(err, "Error accessing 'requirements' field in NodePool", "name", nodePool.GetName())
	// 		continue
	// 	}
	// 	if !found {
	// 		logger.Info("'requirements' field not found in NodePool", "name", nodePool.GetName())
	// 		continue
	// 	}
	//
	// 	logger.Info("NodePool Requirements", "requirements", requirements)
	// }

	return nodePools, nil
}

// Reconcile is part of the main Kubernetes reconciliation loop
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Pod instance
	pod := &corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Pod not found. It might have been deleted after the reconcile request.
			logger.Info("Pod not found. Ignoring since object must be deleted.", "pod", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get Pod", "pod", req.NamespacedName)
		return ctrl.Result{}, err
	}

	// Check if the pod is pending
	if pod.Status.Phase == corev1.PodPending {
		logger.Info("Pod is pending", "pod", req.NamespacedName)

		// Log NodeSelectors and prepare for matching
		nodeSelector := pod.Spec.NodeSelector
		if nodeSelector != nil {
			for key, value := range nodeSelector {
				logger.Info("NodeSelector", "key", key, "value", value)
			}
		} else {
			logger.Info("No NodeSelector defined for this pod", "pod", req.NamespacedName)
		}

		// List NodePools
		nodePools, err := r.listNodePools(ctx)
		if err != nil {
			logger.Error(err, "Failed to list NodePools")
			return ctrl.Result{}, err
		}

		// Iterate over NodeSelectors and find matching NodePools
		if nodeSelector != nil {
			for nodeSelectorkey, nodeSelectorValue := range nodeSelector {
				found := false
				for _, nodePool := range nodePools {
					// Extract 'taints' field from NodePool
					taints, foundTaints, err := unstructured.NestedSlice(nodePool.Object, "spec", "template", "spec", "taints")
					if err != nil {
						logger.Error(err, "Error accessing 'taints' field in NodePool", "name", nodePool.GetName())
						continue
					}
					if !foundTaints {
						logger.Info("'taints' field not found in NodePool", "name", nodePool.GetName())
						continue
					}

					// Iterate over taints to find a match
					for _, taint := range taints {
						taintMap, ok := taint.(map[string]interface{})
						if !ok {
							logger.Error(fmt.Errorf("taint is not a map"), "Invalid taint format in NodePool", "name", nodePool.GetName())
							continue
						}

						taintKey, foundKey, err := unstructured.NestedString(taintMap, "key")
						if err != nil || !foundKey {
							logger.Error(err, "Error accessing 'key' in taint", "name", nodePool.GetName())
							continue
						}

						taintValue, foundValue, err := unstructured.NestedString(taintMap, "value")
						if err != nil || !foundValue {
							logger.Error(err, "Error accessing 'value' in taint", "name", nodePool.GetName())
							continue
						}

						// Check if taint key and value match the node selector
						if taintKey == nodeSelectorkey && taintValue == nodeSelectorValue {
							logger.Info("Matching NodePool found", "NodePool", nodePool.GetName())
							found = true
							break
						}
					}

					if found {
						break // Stop searching other NodePools once a match is found for this key-value
					}
				}

				if !found {
					logger.Info("Cannot find a NodePool matching the NodeSelector")
				}
			}
		}

		// Example: Requeue after 1 minute to re-evaluate the Pod's status
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// If Pod is not pending, no action is required
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
