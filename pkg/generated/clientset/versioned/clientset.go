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

package versioned

import (
	"fmt"

	appsv1alpha1 "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/typed/apps/v1alpha1"
	corev1alpha1 "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/typed/core/v1alpha1"
	networkingv1alpha1 "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/typed/networking/v1alpha1"
	registrationv1alpha1 "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/typed/registration/v1alpha1"
	discovery "k8s.io/client-go/discovery"
	rest "k8s.io/client-go/rest"
	flowcontrol "k8s.io/client-go/util/flowcontrol"
)

type Interface interface {
	Discovery() discovery.DiscoveryInterface
	AppsV1alpha1() appsv1alpha1.AppsV1alpha1Interface
	CoreV1alpha1() corev1alpha1.CoreV1alpha1Interface
	NetworkingV1alpha1() networkingv1alpha1.NetworkingV1alpha1Interface
	RegistrationV1alpha1() registrationv1alpha1.RegistrationV1alpha1Interface
}

// Clientset contains the clients for groups. Each group has exactly one
// version included in a Clientset.
type Clientset struct {
	*discovery.DiscoveryClient
	appsV1alpha1         *appsv1alpha1.AppsV1alpha1Client
	coreV1alpha1         *corev1alpha1.CoreV1alpha1Client
	networkingV1alpha1   *networkingv1alpha1.NetworkingV1alpha1Client
	registrationV1alpha1 *registrationv1alpha1.RegistrationV1alpha1Client
}

// AppsV1alpha1 retrieves the AppsV1alpha1Client
func (c *Clientset) AppsV1alpha1() appsv1alpha1.AppsV1alpha1Interface {
	return c.appsV1alpha1
}

// CoreV1alpha1 retrieves the CoreV1alpha1Client
func (c *Clientset) CoreV1alpha1() corev1alpha1.CoreV1alpha1Interface {
	return c.coreV1alpha1
}

// NetworkingV1alpha1 retrieves the NetworkingV1alpha1Client
func (c *Clientset) NetworkingV1alpha1() networkingv1alpha1.NetworkingV1alpha1Interface {
	return c.networkingV1alpha1
}

// RegistrationV1alpha1 retrieves the RegistrationV1alpha1Client
func (c *Clientset) RegistrationV1alpha1() registrationv1alpha1.RegistrationV1alpha1Interface {
	return c.registrationV1alpha1
}

// Discovery retrieves the DiscoveryClient
func (c *Clientset) Discovery() discovery.DiscoveryInterface {
	if c == nil {
		return nil
	}
	return c.DiscoveryClient
}

// NewForConfig creates a new Clientset for the given config.
// If config's RateLimiter is not set and QPS and Burst are acceptable,
// NewForConfig will generate a rate-limiter in configShallowCopy.
func NewForConfig(c *rest.Config) (*Clientset, error) {
	configShallowCopy := *c
	if configShallowCopy.RateLimiter == nil && configShallowCopy.QPS > 0 {
		if configShallowCopy.Burst <= 0 {
			return nil, fmt.Errorf("burst is required to be greater than 0 when RateLimiter is not set and QPS is set to greater than 0")
		}
		configShallowCopy.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(configShallowCopy.QPS, configShallowCopy.Burst)
	}
	var cs Clientset
	var err error
	cs.appsV1alpha1, err = appsv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.coreV1alpha1, err = corev1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.networkingV1alpha1, err = networkingv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.registrationV1alpha1, err = registrationv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}

	cs.DiscoveryClient, err = discovery.NewDiscoveryClientForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

// NewForConfigOrDie creates a new Clientset for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *Clientset {
	var cs Clientset
	cs.appsV1alpha1 = appsv1alpha1.NewForConfigOrDie(c)
	cs.coreV1alpha1 = corev1alpha1.NewForConfigOrDie(c)
	cs.networkingV1alpha1 = networkingv1alpha1.NewForConfigOrDie(c)
	cs.registrationV1alpha1 = registrationv1alpha1.NewForConfigOrDie(c)

	cs.DiscoveryClient = discovery.NewDiscoveryClientForConfigOrDie(c)
	return &cs
}

// New creates a new Clientset for the given RESTClient.
func New(c rest.Interface) *Clientset {
	var cs Clientset
	cs.appsV1alpha1 = appsv1alpha1.New(c)
	cs.coreV1alpha1 = corev1alpha1.New(c)
	cs.networkingV1alpha1 = networkingv1alpha1.New(c)
	cs.registrationV1alpha1 = registrationv1alpha1.New(c)

	cs.DiscoveryClient = discovery.NewDiscoveryClient(c)
	return &cs
}
