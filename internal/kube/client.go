package kube

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

type Options struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

func (c *Client) Patch(ctx context.Context, gvr schema.GroupVersionResource, object unstructured.Unstructured, patch []byte) error {
	_, err := c.dynamic.Resource(gvr).Namespace(object.GetNamespace()).Patch(ctx, object.GetName(), types.JSONPatchType, patch, metav1.PatchOptions{})
	return err
}

func (c *Client) Delete(ctx context.Context, gvr schema.GroupVersionResource, object unstructured.Unstructured) error {
	options := metav1.DeleteOptions{}
	preconditions := &metav1.Preconditions{}
	if uid := object.GetUID(); uid != "" {
		preconditions.UID = &uid
	}
	if version := object.GetResourceVersion(); version != "" {
		preconditions.ResourceVersion = &version
	}
	if preconditions.UID != nil || preconditions.ResourceVersion != nil {
		options.Preconditions = preconditions
	}
	return c.dynamic.Resource(gvr).Namespace(object.GetNamespace()).Delete(ctx, object.GetName(), options)
}

type Client struct {
	dynamic dynamic.Interface
}

// Connect uses standard kubeconfig loading/merging; it sends no API requests.
func Connect(options Options) (*Client, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = options.Kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: options.Context}
	overrides.Context.Namespace = options.Namespace
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	namespace, _, err := config.Namespace()
	if err != nil {
		return nil, "", err
	}
	restConfig, err := config.ClientConfig()
	if err != nil {
		return nil, "", err
	}
	restConfig.Timeout = 30 * time.Second
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, "", err
	}
	return &Client{dynamic: client}, namespace, nil
}

func (c *Client) List(ctx context.Context, gvr schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	resource := c.dynamic.Resource(gvr).Namespace(namespace)
	options := metav1.ListOptions{Limit: 500}
	objects := make([]unstructured.Unstructured, 0)
	for {
		page, err := resource.List(ctx, options)
		if err != nil {
			return nil, err
		}
		objects = append(objects, page.Items...)
		next := page.GetContinue()
		if next == "" {
			return objects, nil
		}
		if next == options.Continue {
			return nil, fmt.Errorf("API returned a repeated pagination token")
		}
		options.Continue = next
	}
}
