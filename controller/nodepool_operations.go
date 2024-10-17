package controller

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// findMatchingNodePool searches for an existing NodePool matching the 'provision-for-team' taint
func (r *PodReconciler) findMatchingNodePool(ctx context.Context, provisionForTeamValue string) (bool, error) {
	logger := log.FromContext(ctx)

	nodePools, err := r.listNodePools(ctx)
	if err != nil {
		logger.Error(err, "Failed to list NodePools")
		return false, err
	}

	for _, nodePool := range nodePools {
		taints, foundTaints, err := unstructured.NestedSlice(nodePool.Object, "spec", "template", "spec", "taints")
		if err != nil || !foundTaints {
			logger.Error(err, "Error accessing 'taints' field in NodePool", "name", nodePool.GetName())
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

			if taintValue == provisionForTeamValue {
				logger.Info("Matching NodePool found", "NodePool", nodePool.GetName())
				return true, nil
			}
		}
	}

	logger.Info("No matching NodePool found for 'provision-for-team'", "value", provisionForTeamValue)
	return false, nil
}

// listNodePools lists NodePool objects using the dynamic client
func (r *PodReconciler) listNodePools(ctx context.Context) ([]unstructured.Unstructured, error) {
	logger := log.FromContext(ctx)

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

	return nodePoolsList.Items, nil
}

// createNodePool creates a new NodePool with the specified taint value
func (r *PodReconciler) createNodePool(ctx context.Context, provisionForTeamValue string) error {
	logger := log.FromContext(ctx)

	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	nodePoolName := fmt.Sprintf("nodepool-%s", provisionForTeamValue)

	nodePool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": nodePoolName,
			},
			"spec": map[string]interface{}{
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

	_, err := r.DynamicClient.Resource(gvr).Namespace("").Create(ctx, nodePool, metav1.CreateOptions{})
	if err != nil {
		logger.Error(err, "Failed to create NodePool", "name", nodePoolName)
		return fmt.Errorf("failed to create NodePool: %w", err)
	}

	logger.Info("Successfully created NodePool", "name", nodePoolName)
	return nil
}
