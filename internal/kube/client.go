package kube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
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
	dynamic   dynamic.Interface
	discovery discovery.DiscoveryInterface
}

func (c *Client) Create(ctx context.Context, gvr schema.GroupVersionResource, object unstructured.Unstructured) error {
	_, err := c.dynamic.Resource(gvr).Namespace(object.GetNamespace()).Create(ctx, &object, metav1.CreateOptions{})
	return err
}

// Connect loads standard kubeconfig without API requests and binds discovery's
// context-less methods to this invocation's cancellation and deadline.
func Connect(ctx context.Context, options Options) (*Client, string, error) {
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
	restConfig.WrapTransport = func(next http.RoundTripper) http.RoundTripper { return contextTransport{ctx: ctx, next: next} }
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, "", err
	}
	discover, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, "", err
	}
	return &Client{dynamic: client, discovery: discover}, namespace, nil
}

type contextTransport struct {
	ctx  context.Context
	next http.RoundTripper
}

func (t contextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Keep the dynamic request's own context too; discovery uses Background.
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	response, err := t.next.RoundTrip(r.WithContext(ctx))
	cleanup := func() { stop(); cancel() }
	if err != nil {
		cleanup()
		return nil, err
	}
	response.Body = &cancelBody{ReadCloser: response.Body, cleanup: cleanup}
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *cancelBody) Close() error { defer b.cleanup(); return b.ReadCloser.Close() }

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
