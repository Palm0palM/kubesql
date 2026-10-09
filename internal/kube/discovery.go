package kube

import (
	"context"
	"errors"
	"strings"

	"github.com/Palm0palM/kubesql/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Snapshot keeps discovery order and partial failures for this one statement only.
type Snapshot struct {
	Groups []*metav1.APIGroup
	Lists  []*metav1.APIResourceList
	Failed map[schema.GroupVersion]error
}

func discoveryError(code, message string) error { return &resource.Error{Code: code, Message: message} }

func explicitGVR(name string) (schema.GroupVersionResource, error) {
	parts := strings.Split(name, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return schema.GroupVersionResource{Version: parts[0], Resource: parts[1]}, nil
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "" {
		return schema.GroupVersionResource{Group: parts[0], Version: parts[1], Resource: parts[2]}, nil
	}
	return schema.GroupVersionResource{}, discoveryError("E_TABLE", "expected version/resource or group/version/resource; subresources are unsupported")
}

func descriptor(list *metav1.APIResourceList, name string) (resource.Descriptor, bool) {
	gv, err := schema.ParseGroupVersion(list.GroupVersion)
	if err != nil {
		return resource.Descriptor{}, false
	}
	for _, r := range list.APIResources {
		if r.Name == name && !strings.Contains(r.Name, "/") {
			return resource.Descriptor{GVR: gv.WithResource(r.Name), Kind: r.Kind, Namespaced: r.Namespaced, Verbs: append([]string{}, r.Verbs...)}, true
		}
	}
	return resource.Descriptor{}, false
}

// Resolve never guesses plurals, short names or versions. Failed groups make bare
// names unsafe: their ambiguity cannot be ruled out. Exact healthy versions work.
func (s Snapshot) Resolve(name string, quoted bool) (resource.Descriptor, error) {
	if quoted {
		gvr, err := explicitGVR(name)
		if err != nil {
			return resource.Descriptor{}, err
		}
		if _, failed := s.Failed[gvr.GroupVersion()]; failed {
			return resource.Descriptor{}, discoveryError("E_DISCOVERY", "target group/version discovery failed")
		}
		for _, list := range s.Lists {
			if list.GroupVersion == gvr.GroupVersion().String() {
				if d, ok := descriptor(list, gvr.Resource); ok {
					return d, nil
				}
			}
		}
		return resource.Descriptor{}, discoveryError("E_UNKNOWN_TABLE", "resource is not served at the requested version")
	}
	if strings.Contains(name, "/") {
		return resource.Descriptor{}, discoveryError("E_TABLE", "subresources and unquoted exact resource names are unsupported")
	}
	if len(s.Failed) != 0 {
		return resource.Descriptor{}, discoveryError("E_DISCOVERY", "partial discovery cannot rule out resource ambiguity; use an exact table name")
	}
	byGroup := map[string]map[string]resource.Descriptor{}
	for _, list := range s.Lists {
		if d, ok := descriptor(list, name); ok {
			if byGroup[d.GVR.Group] == nil {
				byGroup[d.GVR.Group] = map[string]resource.Descriptor{}
			}
			byGroup[d.GVR.Group][d.GVR.Version] = d
		}
	}
	if len(byGroup) == 0 {
		return resource.Descriptor{}, discoveryError("E_UNKNOWN_TABLE", "unknown resource table")
	}
	if len(byGroup) > 1 {
		return resource.Descriptor{}, discoveryError("E_AMBIGUOUS_TABLE", "resource name occurs in multiple API groups; use an exact table name")
	}
	for group, versions := range byGroup {
		for _, g := range s.Groups {
			if g.Name != group {
				continue
			}
			if d, ok := versions[g.PreferredVersion.Version]; ok {
				return d, nil
			}
			for _, version := range g.Versions {
				if d, ok := versions[version.Version]; ok {
					return d, nil
				}
			}
		}
		// Core API can be absent from the APIGroup list returned by older servers.
		if group == "" {
			for _, list := range s.Lists {
				if d, ok := descriptor(list, name); ok && d.GVR.Group == "" {
					return d, nil
				}
			}
		}
	}
	return resource.Descriptor{}, discoveryError("E_DISCOVERY", "discovery did not supply resource version priority")
}

func (c *Client) Resolve(ctx context.Context, name string, quoted bool) (resource.Descriptor, error) {
	// Discovery methods do not accept context. The per-statement client carries it
	// through its HTTP transport, so cancellation/deadlines still cover discovery.
	if err := ctx.Err(); err != nil {
		return resource.Descriptor{}, err
	}
	if quoted {
		gvr, err := explicitGVR(name)
		if err != nil {
			return resource.Descriptor{}, err
		}
		list, err := c.discovery.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
		if err != nil {
			return resource.Descriptor{}, discoveryError("E_DISCOVERY", "target group/version discovery failed")
		}
		return (Snapshot{Lists: []*metav1.APIResourceList{list}}).Resolve(name, true)
	}
	groups, lists, err := c.discovery.ServerGroupsAndResources()
	s := Snapshot{Groups: groups, Lists: lists}
	if err != nil {
		var partial *discovery.ErrGroupDiscoveryFailed
		if !errors.As(err, &partial) {
			return resource.Descriptor{}, discoveryError("E_DISCOVERY", "API discovery failed")
		}
		s.Failed = partial.Groups
	}
	return s.Resolve(name, false)
}
