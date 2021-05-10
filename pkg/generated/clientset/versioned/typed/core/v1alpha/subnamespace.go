/*
Copyright The Kubernetes Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package v1alpha

import (
	"context"
	"time"

	v1alpha "github.com/EdgeNet-project/edgenet/pkg/apis/core/v1alpha"
	scheme "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// SubNamespacesGetter has a method to return a SubNamespaceInterface.
// A group's client should implement this interface.
type SubNamespacesGetter interface {
	SubNamespaces(namespace string) SubNamespaceInterface
}

// SubNamespaceInterface has methods to work with SubNamespace resources.
type SubNamespaceInterface interface {
	Create(ctx context.Context, subNamespace *v1alpha.SubNamespace, opts v1.CreateOptions) (*v1alpha.SubNamespace, error)
	Update(ctx context.Context, subNamespace *v1alpha.SubNamespace, opts v1.UpdateOptions) (*v1alpha.SubNamespace, error)
	UpdateStatus(ctx context.Context, subNamespace *v1alpha.SubNamespace, opts v1.UpdateOptions) (*v1alpha.SubNamespace, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha.SubNamespace, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha.SubNamespaceList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha.SubNamespace, err error)
	SubNamespaceExpansion
}

// subNamespaces implements SubNamespaceInterface
type subNamespaces struct {
	client rest.Interface
	ns     string
}

// newSubNamespaces returns a SubNamespaces
func newSubNamespaces(c *CoreV1alphaClient, namespace string) *subNamespaces {
	return &subNamespaces{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the subNamespace, and returns the corresponding subNamespace object, and an error if there is any.
func (c *subNamespaces) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha.SubNamespace, err error) {
	result = &v1alpha.SubNamespace{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("subnamespaces").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of SubNamespaces that match those selectors.
func (c *subNamespaces) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha.SubNamespaceList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha.SubNamespaceList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("subnamespaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested subNamespaces.
func (c *subNamespaces) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("subnamespaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a subNamespace and creates it.  Returns the server's representation of the subNamespace, and an error, if there is any.
func (c *subNamespaces) Create(ctx context.Context, subNamespace *v1alpha.SubNamespace, opts v1.CreateOptions) (result *v1alpha.SubNamespace, err error) {
	result = &v1alpha.SubNamespace{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("subnamespaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(subNamespace).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a subNamespace and updates it. Returns the server's representation of the subNamespace, and an error, if there is any.
func (c *subNamespaces) Update(ctx context.Context, subNamespace *v1alpha.SubNamespace, opts v1.UpdateOptions) (result *v1alpha.SubNamespace, err error) {
	result = &v1alpha.SubNamespace{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("subnamespaces").
		Name(subNamespace.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(subNamespace).
		Do(ctx).
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *subNamespaces) UpdateStatus(ctx context.Context, subNamespace *v1alpha.SubNamespace, opts v1.UpdateOptions) (result *v1alpha.SubNamespace, err error) {
	result = &v1alpha.SubNamespace{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("subnamespaces").
		Name(subNamespace.Name).
		SubResource("status").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(subNamespace).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the subNamespace and deletes it. Returns an error if one occurs.
func (c *subNamespaces) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("subnamespaces").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *subNamespaces) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Namespace(c.ns).
		Resource("subnamespaces").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched subNamespace.
func (c *subNamespaces) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha.SubNamespace, err error) {
	result = &v1alpha.SubNamespace{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("subnamespaces").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}
