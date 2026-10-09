// Package resource contains the small discovery contract shared by kube and engine.
package resource

import "k8s.io/apimachinery/pkg/runtime/schema"

type Descriptor struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
	Verbs      []string
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
