/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// straight copy of https://github.com/kubernetes-sigs/controller-runtime/blob/df2c43d8896d99e309f7da9d8fd16ad7a21f70a2/pkg/client/client_cache.go
// as the c-r client doesn't have watch.
package watchingclient

import (
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// clientCache creates and caches rest clients and metadata for Kubernetes types
type clientCache struct {
	// config is the rest.Config to talk to an apiserver
	config *rest.Config

	// scheme maps go structs to GroupVersionKinds
	scheme *runtime.Scheme

	// mapper maps GroupVersionKinds to Resources
	mapper meta.RESTMapper

	// codecs are used to create a REST client for a gvk
	codecs serializer.CodecFactory

	// structuredResourceByType caches structured type metadata
	structuredResourceByType map[schema.GroupVersionKind]*resourceMeta
	// unstructuredResourceByType caches unstructured type metadata
	unstructuredResourceByType map[schema.GroupVersionKind]*resourceMeta
	mu                         sync.RWMutex
}

// newResource maps obj to a Kubernetes Resource and constructs a client for that Resource.
// If the object is a list, the resource represents the item's type instead.
func (c *clientCache) newResource(gvk schema.GroupVersionKind, isList, isUnstructured bool) (*resourceMeta, error) {
	if strings.HasSuffix(gvk.Kind, "List") && isList {
		// if this was a list, treat it as a request for the item's resource
		gvk.Kind = gvk.Kind[:len(gvk.Kind)-4]
	}

	client, err := apiutil.RESTClientForGVK(gvk, isUnstructured, c.config, c.codecs)
	if err != nil {
		return nil, err
	}
	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	return &resourceMeta{Interface: client, mapping: mapping, gvk: gvk}, nil
}

// getResource returns the resource meta information for the given type of object.
// If the object is a list, the resource represents the item's type instead.
func (c *clientCache) getResource(obj runtime.Object) (*resourceMeta, error) {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return nil, err
	}

	_, isUnstructured := obj.(*unstructured.Unstructured)
	_, isUnstructuredList := obj.(*unstructured.UnstructuredList)
	isUnstructured = isUnstructured || isUnstructuredList

	// It's better to do creation work twice than to not let multiple
	// people make requests at once
	c.mu.RLock()
	resourceByType := c.structuredResourceByType
	if isUnstructured {
		resourceByType = c.unstructuredResourceByType
	}
	r, known := resourceByType[gvk]
	c.mu.RUnlock()

	if known {
		return r, nil
	}

	// Initialize a new Client
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err = c.newResource(gvk, meta.IsListType(obj), isUnstructured)
	if err != nil {
		return nil, err
	}
	resourceByType[gvk] = r
	return r, err
}

// resourceMeta caches state for a Kubernetes type.
type resourceMeta struct {
	// client is the rest client used to talk to the apiserver
	rest.Interface
	// gvk is the GroupVersionKind of the resourceMeta
	gvk schema.GroupVersionKind
	// mapping is the rest mapping
	mapping *meta.RESTMapping
}

// isNamespaced returns true if the type is namespaced
func (r *resourceMeta) isNamespaced() bool {
	return r.mapping.Scope.Name() != meta.RESTScopeNameRoot

}

// resource returns the resource name of the type
func (r *resourceMeta) resource() string {
	return r.mapping.Resource.Resource
}
