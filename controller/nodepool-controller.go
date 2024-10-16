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

	if r.DynamicClient == nil {
		err := fmt.Errorf("dynamic client is nil")
		logger.Error(err, "Dynamic client is not initialized")
		return nil, err
	}

	nodePoolsList, err := r.DynamicClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list NodePools: %w", err)
	}

	nodePools := nodePoolsList.Items

	return nodePools, nil
}

// createNodePool creates a new NodePool with the specified taint value
func (r *PodReconciler) createNodePool(ctx context.Context, provisionForTeamValue string) error {
	logger := log.FromContext(ctx)

	// Define the GVR for the NodePool CRD
	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Define the NodePool name
	nodePoolName := fmt.Sprintf("nodepool-%s", provisionForTeamValue)

	// Define the NodePool object
	nodePool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": nodePoolName,
			},
			"spec": map[string]interface{}{
				// Define the necessary spec fields based on your requirements
				"limits": map[string]interface{}{
					"cpu":    "12000m",
					"memory": "64Gi",
				},
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"taints": []interface{}{
							map[string]interface{}{
								"key":    "provision-for-team",
								"value":  provisionForTeamValue,
								"effect": "NoSchedule",
							},
						},
						"nodeClassRef": map[string]interface{}{
							"group": "karpenter.k8s.aws",
							"kind":  "EC2NodeClass",
							"name":  "custom",
						},
						"requirements": []interface{}{
							map[string]interface{}{
								"key":      "karpenter.sh/capacity-type",
								"operator": "In",
								"values":   []interface{}{"spot"},
							},
						},
						"expireAfter": "24h",
					},
				},
				"disruption": map[string]interface{}{
					"budgets": []interface{}{
						map[string]interface{}{
							"nodes": "10%",
						},
					},
					"consolidateAfter":    "10m",
					"consolidationPolicy": "WhenEmpty",
				},
			},
		},
	}

	// Create the NodePool
	_, err := r.DynamicClient.Resource(gvr).Namespace("").Create(ctx, nodePool, metav1.CreateOptions{})
	if err != nil {
		logger.Error(err, "Failed to create NodePool", "name", nodePoolName)
		return fmt.Errorf("failed to create NodePool: %w", err)
	}

	logger.Info("Successfully created NodePool", "name", nodePoolName)
	return nil
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

		// Retrieve the nodeSelector map from the Pod spec
		nodeSelector := pod.Spec.NodeSelector

		// Look for the 'provision-for-team' key
		provisionForTeamValue, exists := nodeSelector["provision-for-team"]
		if exists {
			logger.Info("Found NodeSelector 'provision-for-team'", "value", provisionForTeamValue)
		} else {
			logger.Info("'provision-for-team' key not found in NodeSelector")
			return ctrl.Result{}, nil
		}

		// List NodePools
		nodePools, err := r.listNodePools(ctx)
		if err != nil {
			logger.Error(err, "Failed to list NodePools")
			return ctrl.Result{}, err
		}

		// Flag to indicate if a matching NodePool was found
		matchingNodePoolFound := false

		// Iterate over NodePools to find a match based on taints
		for _, nodePool := range nodePools {
			taints, foundTaints, err := unstructured.NestedSlice(nodePool.Object, "spec", "template", "spec", "taints")
			if err != nil {
				logger.Error(err, "Error accessing 'taints' field in NodePool", "name", nodePool.GetName())
				continue
			}
			if !foundTaints {
				logger.Info("'taints' field not found in NodePool", "name", nodePool.GetName())
				continue
			}

			for _, taint := range taints {
				taintMap, ok := taint.(map[string]interface{})
				if !ok {
					logger.Error(fmt.Errorf("taint is not a map"), "Invalid taint format in NodePool", "name", nodePool.GetName())
					continue
				}

				taintValue, foundValue, err := unstructured.NestedString(taintMap, "value")
				if err != nil || !foundValue {
					logger.Error(err, "Error accessing 'value' in taint", "name", nodePool.GetName())
					continue
				}

				logger.Info("Taint Value", "value", taintValue)

				if taintValue == provisionForTeamValue {
					logger.Info("Matching NodePool found", "NodePool", nodePool.GetName())
					matchingNodePoolFound = true
					break
				}
			}

			if matchingNodePoolFound {
				break
			}
		}

		if !matchingNodePoolFound {
			logger.Info("Cannot find a NodePool matching the NodeSelector 'provision-for-team'", "value", provisionForTeamValue)
			logger.Info("Creating a new NodePool")
			err := r.createNodePool(ctx, provisionForTeamValue)
			if err != nil {
				logger.Error(err, "Failed to create NodePool")
				return ctrl.Result{}, err
			}

			// Optionally, you might want to requeue immediately to verify the NodePool creation
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// Example: Requeue after 1 minute to re-evaluate the Pod's status
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
