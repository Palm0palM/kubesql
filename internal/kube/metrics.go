package kube

import (
	"context"
	"errors"
	"math/big"

	shared "github.com/Palm0palM/kubesql/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func metricsError(code, message string) error { return &shared.Error{Code: code, Message: message} }

// Metrics reads the real metrics.k8s.io API through the existing official dynamic
// client. Quantity, not requests/limits or string arithmetic, defines aggregation.
func (c *Client) Metrics(ctx context.Context, pods bool, namespace string) ([]unstructured.Unstructured, error) {
	name := "nodes"
	if pods {
		name = "pods"
	} else {
		namespace = ""
	}
	objects, err := c.List(ctx, schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: name}, namespace)
	if err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, metricsError("E_METRICS_UNAVAILABLE", "Metrics API is unavailable")
	}
	rows := make([]unstructured.Unstructured, 0, len(objects))
	for _, object := range objects {
		row, err := summarizeMetric(object, pods)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func summarizeMetric(object unstructured.Unstructured, pods bool) (unstructured.Unstructured, error) {
	invalid := func() (unstructured.Unstructured, error) {
		return unstructured.Unstructured{}, metricsError("E_METRICS_DATA", "Metrics API returned an invalid sample")
	}
	if object.GetName() == "" || (pods && object.GetNamespace() == "") {
		return invalid()
	}
	var usages []map[string]any
	if pods {
		containers, found, err := unstructured.NestedSlice(object.Object, "containers")
		if err != nil || !found || len(containers) == 0 {
			return invalid()
		}
		for _, raw := range containers {
			container, ok := raw.(map[string]any)
			if !ok {
				return invalid()
			}
			usage, ok := container["usage"].(map[string]any)
			if !ok {
				return invalid()
			}
			usages = append(usages, usage)
		}
	} else {
		usage, found, err := unstructured.NestedMap(object.Object, "usage")
		if err != nil || !found {
			return invalid()
		}
		usages = []map[string]any{usage}
	}
	var cpu, memory resource.Quantity
	for _, usage := range usages {
		cpuText, cpuOK := usage["cpu"].(string)
		memText, memOK := usage["memory"].(string)
		if !cpuOK || !memOK {
			return invalid()
		}
		c, cpuErr := resource.ParseQuantity(cpuText)
		m, memErr := resource.ParseQuantity(memText)
		if cpuErr != nil || memErr != nil || c.Sign() < 0 || m.Sign() < 0 {
			return invalid()
		}
		cpu.Add(c)
		memory.Add(m)
	}
	// Guard int64 conversion: Quantity.Value/MilliValue can overflow silently.
	limit := new(big.Rat).SetInt64(9223372036854775807)
	cpuRat, cpuOK := new(big.Rat).SetString(cpu.AsDec().String())
	memRat, memOK := new(big.Rat).SetString(memory.AsDec().String())
	if !cpuOK || !memOK || new(big.Rat).Mul(cpuRat, big.NewRat(1000, 1)).Cmp(limit) > 0 || memRat.Cmp(limit) > 0 {
		return invalid()
	}
	metadata := map[string]any{"name": object.GetName()}
	if pods {
		metadata["namespace"] = object.GetNamespace()
	}
	return unstructured.Unstructured{Object: map[string]any{"metadata": metadata, "cpu_millicores": cpu.MilliValue(), "memory_bytes": memory.Value()}}, nil
}
