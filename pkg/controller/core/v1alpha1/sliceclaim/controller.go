/*
Copyright 2021 Contributors to the EdgeNet project.

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

package sliceclaim

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1alpha1 "github.com/EdgeNet-project/edgenet/pkg/apis/core/v1alpha1"
	clientset "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned"
	"github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/scheme"
	edgenetscheme "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/scheme"
	informers "github.com/EdgeNet-project/edgenet/pkg/generated/informers/externalversions/core/v1alpha1"
	listers "github.com/EdgeNet-project/edgenet/pkg/generated/listers/core/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const controllerAgentName = "sliceclaim-controller"

// Definitions of the state of the sliceclaim resource
const (
	successSynced         = "Synced"
	messageResourceSynced = "Slice claim synced successfully"
	successClaimed        = "Slice Claimed"
	messageClaimed        = "Slice claimed successfully"
	successApplied        = "Applied"
	messageApplied        = "Slice claim has applied successfully"
	successQuotaCheck     = "Checked"
	messageQuotaCheck     = "The parent has sufficient quota"
	successBound          = "Bound"
	messageBound          = "Slice is bound successfully"
	failureQuotaShortage  = "Shortage"
	messageQuotaShortage  = "Insufficient quota at the parent"
	failureBound          = "Already Bound"
	messageBoundAlready   = "Slice is bound to another claim already"
	failureBinding        = "Binding Failed"
	messageBindingFailed  = "Slice binding failed"
	failureCreation       = "Creation Failed"
	messageCreationFailed = "Slice creation failed"
	pendingSlice          = "Not Bound"
	messagePending        = "Waiting for the slice"
	dynamic               = "Dynamic"
	// manual                = "Manual"
	failure     = "Failure"
	pending     = "Pending"
	requested   = "Requested"
	bound       = "Bound"
	applied     = "Applied"
	reserved    = "Reserved"
	established = "Established"
)

// Controller is the controller implementation for Slice Claimresources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// edgenetclientset is a clientset for the EdgeNet API groups
	edgenetclientset clientset.Interface

	sliceclaimsLister listers.SliceClaimLister
	sliceclaimsSynced cache.InformerSynced

	subnamespacesLister listers.SubNamespaceLister
	subnamespacesSynced cache.InformerSynced

	provisioning string

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new controller
func NewController(
	kubeclientset kubernetes.Interface,
	edgenetclientset clientset.Interface,
	subnamespaceInformer informers.SubNamespaceInformer,
	sliceclaimInformer informers.SliceClaimInformer,
	provisioning string) *Controller {

	utilruntime.Must(edgenetscheme.AddToScheme(scheme.Scheme))
	klog.Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:       kubeclientset,
		edgenetclientset:    edgenetclientset,
		subnamespacesLister: subnamespaceInformer.Lister(),
		subnamespacesSynced: subnamespaceInformer.Informer().HasSynced,
		sliceclaimsLister:   sliceclaimInformer.Lister(),
		sliceclaimsSynced:   sliceclaimInformer.Informer().HasSynced,
		workqueue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SliceClaims"),
		recorder:            recorder,
		provisioning:        provisioning,
	}

	klog.Infoln("Setting up event handlers")
	// Set up an event handler for when Slice Claimresources change
	sliceclaimInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueSliceClaim,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueSliceClaim(new)
		},
	})

	subnamespaceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleSubNamespace,
		UpdateFunc: func(old, new interface{}) {
			newObj := new.(*corev1alpha1.SubNamespace)
			oldObj := old.(*corev1alpha1.SubNamespace)
			if newObj.ResourceVersion == oldObj.ResourceVersion {
				return
			}
			controller.handleSubNamespace(new)
		},
		DeleteFunc: controller.handleSubNamespace,
	})

	return controller
}

// Run will set up the event handlers for the types of slice claim and node, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	klog.Infoln("Starting Slice Claimcontroller")

	klog.Infoln("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.subnamespacesSynced,
		c.sliceclaimsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Infoln("Starting workers")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Infoln("Started workers")
	<-stopCh
	klog.Infoln("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueue.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.syncHandler(key); err != nil {
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Slice Claim
// resource with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	sliceclaim, err := c.sliceclaimsLister.SliceClaims(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("sliceclaim '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}
	c.processSliceClaim(sliceclaim.DeepCopy())

	c.recorder.Event(sliceclaim, corev1.EventTypeNormal, successSynced, messageResourceSynced)
	return nil
}

// enqueueSliceClaim takes a Slice Claimresource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Slice Claim.
func (c *Controller) enqueueSliceClaim(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// handleSubNamespace will take any resource implementing corev1alpha1.SubNamespace and attempt
// to find the SliceClaim resource that 'owns' it. It does this by looking at the
// objects SliceClaimRef field. It then enqueues that SliceClaim resource to be processed.
// If the object does not have an appropriate SliceClaimRef, it will simply be skipped.
func (c *Controller) handleSubNamespace(obj interface{}) {
	var subnamespace *corev1alpha1.SubNamespace
	var ok bool
	if subnamespace, ok = obj.(*corev1alpha1.SubNamespace); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding subnamespace, invalid type"))
			return
		}
		subnamespace, ok = tombstone.Obj.(*corev1alpha1.SubNamespace)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding subnamespace tombstone, invalid type"))
			return
		}
		klog.Infof("Recovered deleted subnamespace '%s' from tombstone", subnamespace.GetName())
	}
	klog.Infof("Processing subnamespace: %s", subnamespace.GetName())
	if subnamespace.Status.State != established {
		return
	}
	if name := subnamespace.GetSliceClaim(); name != nil {
		/*if ownerRef := metav1.GetControllerOf(&subnamespace.ObjectMeta); ownerRef != nil {
			if !(ownerRef.Kind == "SliceClaim" && ownerRef.Name == *name) {
				return
			}
		}*/
		sliceclaim, err := c.sliceclaimsLister.SliceClaims(subnamespace.GetNamespace()).Get(*name)
		if err != nil {
			klog.Infof("ignoring orphaned object '%s' of slice claim '%s'", subnamespace.GetSelfLink(), *name)
			return
		}
		c.enqueueSliceClaim(sliceclaim)
	}
}

func (c *Controller) processSliceClaim(sliceclaimCopy *corev1alpha1.SliceClaim) {
	updated := make(chan bool, 1)
	oldStatus := sliceclaimCopy.Status
	updateStatus := func(updated chan<- bool) {
		if !reflect.DeepEqual(oldStatus, sliceclaimCopy.Status) {
			if _, err := c.edgenetclientset.CoreV1alpha1().SliceClaims(sliceclaimCopy.GetNamespace()).UpdateStatus(context.TODO(), sliceclaimCopy, metav1.UpdateOptions{}); err != nil {
				klog.Infoln(err)
			}
		}
		updated <- true
		close(updated)
	}
	defer updateStatus(updated)

	namespace, err := c.kubeclientset.CoreV1().Namespaces().Get(context.TODO(), sliceclaimCopy.GetNamespace(), metav1.GetOptions{})
	if err != nil {
		klog.Infoln(err)
		return
	}

	namespaceLabels := namespace.GetLabels()

	if sliceclaimCopy.Status.State != bound && sliceclaimCopy.Status.State != applied {
		parentResourceQuota, err := c.kubeclientset.CoreV1().ResourceQuotas(sliceclaimCopy.GetNamespace()).Get(context.TODO(), fmt.Sprintf("%s-quota", namespaceLabels["edge-net.io/kind"]), metav1.GetOptions{})
		if err == nil {
			sufficientQuota := c.checkParentResourceQuota(sliceclaimCopy, parentResourceQuota)
			if !sufficientQuota {
				c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, failureQuotaShortage, messageQuotaShortage)
				sliceclaimCopy.Status.State = failure
				sliceclaimCopy.Status.Message = messageQuotaShortage
				return
			}
			c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successQuotaCheck, messageQuotaCheck)
		}

		if done := c.claimSlice(sliceclaimCopy, updated); !done {
			return
		}
	} else {
		if isApplied := c.tieSubnamespace2Claim(sliceclaimCopy); !isApplied && sliceclaimCopy.Status.State != bound {
			c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successBound, messageBound)
			sliceclaimCopy.Status.State = bound
			sliceclaimCopy.Status.Message = messageBound
		}
	}

}

func (c *Controller) claimSlice(sliceclaimCopy *corev1alpha1.SliceClaim, updated <-chan bool) bool {
	var tieClaim2Slice = func(slice *corev1alpha1.Slice) bool {
		if slice.Status.State == failure {
			c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, failureBinding, messageBindingFailed)
			sliceclaimCopy.Status.State = failure
			sliceclaimCopy.Status.Message = messageBindingFailed
		} else {
			if slice.Spec.ClaimRef != nil {
				if reflect.DeepEqual(slice.Spec.ClaimRef, sliceclaimCopy.MakeObjectReference()) {
					c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successBound, messageBound)
					sliceclaimCopy.Status.State = bound
					sliceclaimCopy.Status.Message = messageBound
					return true
				}
				c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, failureBound, messageBoundAlready)
				sliceclaimCopy.Status.State = failure
				sliceclaimCopy.Status.Message = messageBoundAlready
			} else {
				if slice.Status.State == reserved && slice.Spec.SliceClassName == sliceclaimCopy.Spec.SliceClassName && reflect.DeepEqual(slice.Spec.NodeSelector, sliceclaimCopy.Spec.NodeSelector) {
					sliceCopy := slice.DeepCopy()
					sliceCopy.Spec.ClaimRef = sliceclaimCopy.MakeObjectReference()
					if _, err := c.edgenetclientset.CoreV1alpha1().Slices().Update(context.TODO(), sliceCopy, metav1.UpdateOptions{}); err != nil {
						c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, failureBinding, messageBindingFailed)
						sliceclaimCopy.Status.State = failure
						sliceclaimCopy.Status.Message = messageBindingFailed
					}
					c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successClaimed, messageClaimed)
					sliceclaimCopy.Status.State = requested
					sliceclaimCopy.Status.Message = messagePending
				} else {
					c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, failureBinding, messageBindingFailed)
					sliceclaimCopy.Status.State = failure
					sliceclaimCopy.Status.Message = messageBindingFailed
				}
			}
		}
		return false
	}
	if slice, err := c.edgenetclientset.CoreV1alpha1().Slices().Get(context.TODO(), sliceclaimCopy.Spec.SliceName, metav1.GetOptions{}); err != nil {
		if strings.EqualFold(c.provisioning, dynamic) {
			slice = new(corev1alpha1.Slice)
			slice.SetName(sliceclaimCopy.Spec.SliceName)
			slice.Spec.ClaimRef = sliceclaimCopy.MakeObjectReference()
			slice.Status.Expiry = sliceclaimCopy.Spec.SliceExpiry
			slice.Spec.SliceClassName = sliceclaimCopy.Spec.SliceClassName
			slice.Spec.NodeSelector = sliceclaimCopy.Spec.NodeSelector
			if _, err := c.edgenetclientset.CoreV1alpha1().Slices().Create(context.TODO(), slice, metav1.CreateOptions{}); err != nil {
				c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, failureCreation, messageCreationFailed)
				sliceclaimCopy.Status.State = failure
				sliceclaimCopy.Status.Message = messageCreationFailed
			} else if errors.IsAlreadyExists(err) {
				return tieClaim2Slice(slice)
			} else {
				c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successClaimed, messageClaimed)
				sliceclaimCopy.Status.State = requested
				sliceclaimCopy.Status.Message = messagePending
			}
		} else {
			c.recorder.Event(sliceclaimCopy, corev1.EventTypeWarning, pendingSlice, messagePending)
			sliceclaimCopy.Status.State = pending
			sliceclaimCopy.Status.Message = messagePending
		}
	} else {
		return tieClaim2Slice(slice)
	}
	return false
}

func (c *Controller) tieSubnamespace2Claim(sliceclaimCopy *corev1alpha1.SliceClaim) bool {
	if subnamespaceRaw, err := c.edgenetclientset.CoreV1alpha1().SubNamespaces(sliceclaimCopy.GetNamespace()).List(context.TODO(), metav1.ListOptions{}); err == nil {
		for _, subnamespaceRow := range subnamespaceRaw.Items {
			subnamespaceCopy := subnamespaceRow.DeepCopy()
			if name := subnamespaceCopy.GetSliceClaim(); name != nil && *name == sliceclaimCopy.GetName() {
				if ownerRef := metav1.GetControllerOf(subnamespaceCopy); ownerRef != nil {
					if ownerRef.Kind == "SliceClaim" {
						if ownerRef.Name == sliceclaimCopy.GetName() && sliceclaimCopy.Status.State != applied {
							c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successApplied, messageApplied)
							sliceclaimCopy.Status.State = applied
							sliceclaimCopy.Status.Message = messageApplied
							return true
						}
					}
				}
				if subnamespaceCopy.Status.State == established {
					subnamespaceCopy.SetOwnerReferences([]metav1.OwnerReference{sliceclaimCopy.MakeOwnerReference()})
					labelSelector := fmt.Sprintf("edge-net.io/access=public,edge-net.io/pre-reservation=%s", sliceclaimCopy.Spec.SliceName)
					resourceAllocation := make(map[corev1.ResourceName]resource.Quantity)
					if nodeRaw, err := c.kubeclientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: labelSelector}); err == nil {
						for _, nodeRow := range nodeRaw.Items {
							for key, capacity := range nodeRow.Status.Capacity {
								if _, elementExists := resourceAllocation[key]; elementExists {
									resourceQuantity := resourceAllocation[key]
									resourceQuantity.Add(capacity)
									resourceAllocation[key] = resourceQuantity
								} else {
									resourceAllocation[key] = capacity
								}
							}
						}
					}
					subnamespaceCopy.SetResourceAllocation(resourceAllocation)
					if _, err := c.edgenetclientset.CoreV1alpha1().SubNamespaces(subnamespaceCopy.GetNamespace()).Update(context.TODO(), subnamespaceCopy, metav1.UpdateOptions{}); err == nil {
						c.recorder.Event(sliceclaimCopy, corev1.EventTypeNormal, successApplied, messageApplied)
						sliceclaimCopy.Status.State = applied
						sliceclaimCopy.Status.Message = messageApplied
						return true
					}
				}
			}
		}
	}
	return false
}

func (c *Controller) checkParentResourceQuota(sliceclaimCopy *corev1alpha1.SliceClaim, parentResourceQuota *corev1.ResourceQuota) bool {
	var resourceDemandList = make(corev1.ResourceList)
	for key, value := range sliceclaimCopy.Spec.NodeSelector.Resources.Limits {
		resourceDemand := value.DeepCopy()
		for i := 0; i < sliceclaimCopy.Spec.NodeSelector.Count; i++ {
			if _, elementExists := resourceDemandList[key]; elementExists {
				resourceDemand.Add(value)
				resourceDemandList[key] = resourceDemand
			} else {
				resourceDemandList[key] = resourceDemand
			}
		}
	}
	for key, value := range parentResourceQuota.Spec.Hard {
		availableQuota := value.DeepCopy()
		if _, elementExists := resourceDemandList[key]; elementExists {
			if availableQuota.Cmp(resourceDemandList[key]) == -1 {
				return false
			} else {
				availableQuota.Sub(resourceDemandList[key])
			}
		}
	}
	return true
}
