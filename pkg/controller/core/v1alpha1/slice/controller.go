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

package slice

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const controllerAgentName = "slice-controller"

// Definitions of the state of the slice resource
const (
	successSynced         = "Synced"
	messageResourceSynced = "Slice synced successfully"
	successBound          = "Bound"
	messageBound          = "Slice is bound successfully"
	successReserved       = "Reserved"
	messageReserved       = "Desired resources are reserved"
	successExpired        = "Expired"
	messageExpired        = "Slice deleted successfully"
	failureSlice          = "Slice Failed"
	messageSliceFailed    = "There are no adequate resources to slice"
	failurePatch          = "Patch Failed"
	messagePatchFailed    = "Node patch operation has failed"
	failure               = "Failure"
	reserved              = "Reserved"
	bound                 = "Bound"
	provisioned           = "Provisioned"
	applied               = "Applied"
)

// Controller is the controller implementation for Slice resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// edgenetclientset is a clientset for the EdgeNet API groups
	edgenetclientset clientset.Interface

	sliceClaimsLister listers.SliceClaimLister
	sliceClaimsSynced cache.InformerSynced

	slicesLister listers.SliceLister
	slicesSynced cache.InformerSynced

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
	sliceClaimInformer informers.SliceClaimInformer,
	sliceInformer informers.SliceInformer) *Controller {

	utilruntime.Must(edgenetscheme.AddToScheme(scheme.Scheme))
	klog.Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:     kubeclientset,
		edgenetclientset:  edgenetclientset,
		sliceClaimsLister: sliceClaimInformer.Lister(),
		sliceClaimsSynced: sliceClaimInformer.Informer().HasSynced,
		slicesLister:      sliceInformer.Lister(),
		slicesSynced:      sliceInformer.Informer().HasSynced,
		workqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Slices"),
		recorder:          recorder,
	}

	klog.Infoln("Setting up event handlers")
	// Set up an event handler for when Slice resources change
	sliceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueSlice,
		UpdateFunc: func(old, new interface{}) {
			newSlice := new.(*corev1alpha1.Slice)
			oldSlice := old.(*corev1alpha1.Slice)
			if (oldSlice.Status.Expiry == nil && newSlice.Status.Expiry != nil) ||
				((oldSlice.Status.Expiry != nil && newSlice.Status.Expiry != nil) && !oldSlice.Status.Expiry.Time.Equal(newSlice.Status.Expiry.Time)) {
				controller.enqueueSliceAfter(newSlice, time.Until(newSlice.Status.Expiry.Time))
			}
			controller.enqueueSlice(new)
		},
		DeleteFunc: func(obj interface{}) {
			sliceCopy := obj.(*corev1alpha1.Slice).DeepCopy()
			if sliceCopy.Status.State == reserved || sliceCopy.Status.State == bound {
				if nodeRaw, err := controller.kubeclientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("edge-net.io/pre-reservation=%s", sliceCopy.GetName())}); err == nil {
					for _, nodeRow := range nodeRaw.Items {
						controller.patchNode("return", "", nodeRow.GetName())
					}
				}
			}
			if sliceCopy.Status.State == provisioned {
				if nodeRaw, err := controller.kubeclientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("edge-net.io/slice=%s", sliceCopy.GetName())}); err == nil {
					for _, nodeRow := range nodeRaw.Items {
						controller.patchNode("return", "", nodeRow.GetName())
					}
				}
			}
		},
	})

	sliceClaimInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newSliceClaim := new.(*corev1alpha1.SliceClaim)
			oldSliceClaim := old.(*corev1alpha1.SliceClaim)
			if newSliceClaim.ResourceVersion != oldSliceClaim.ResourceVersion {
				controller.handleObject(new)
			}
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for the types of slice and node, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	klog.Infoln("Starting Slice controller")

	klog.Infoln("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.sliceClaimsSynced,
		c.slicesSynced); !ok {
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
// converge the two. It then updates the Status block of the Slice
// resource with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	slice, err := c.slicesLister.Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("slice '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}
	c.processSlice(slice.DeepCopy())

	c.recorder.Event(slice, corev1.EventTypeNormal, successSynced, messageResourceSynced)
	return nil
}

// enqueueSlice takes a Slice resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Slice.
func (c *Controller) enqueueSlice(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// enqueueSliceAfter takes a Slice resource and converts it into a namespace/name
// string which is then put onto the work queue after the expiry date of a claim/drop to delete the so-said claim/drop.
// This method should *not* be passed resources of any type other than Slice.
func (c *Controller) enqueueSliceAfter(obj interface{}, after time.Duration) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.AddAfter(key, after)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the Slice resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that Slice resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "Slice" {
			return
		}

		slice, err := c.slicesLister.Get(ownerRef.Name)
		if err != nil {
			klog.Infof("ignoring orphaned object '%s' of slice '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		c.enqueueSlice(slice)
		return
	}
}

func (c *Controller) processSlice(sliceCopy *corev1alpha1.Slice) {
	if sliceCopy.Status.Expiry != nil && time.Until(sliceCopy.Status.Expiry.Time) <= 0 {
		c.recorder.Event(sliceCopy, corev1.EventTypeWarning, successExpired, messageExpired)
		c.edgenetclientset.CoreV1alpha1().Slices().Delete(context.TODO(), sliceCopy.GetName(), metav1.DeleteOptions{})
		return
	}
	oldStatus := sliceCopy.Status
	statusUpdate := func() {
		if !reflect.DeepEqual(oldStatus, sliceCopy.Status) {
			if _, err := c.edgenetclientset.CoreV1alpha1().Slices().UpdateStatus(context.TODO(), sliceCopy, metav1.UpdateOptions{}); err != nil {
				klog.Infoln(err)
			}
		}
	}
	defer statusUpdate()

	isReserved := c.reserveNodes(sliceCopy)

	if sliceCopy.Spec.ClaimRef != nil {
		if sliceClaim, err := c.edgenetclientset.CoreV1alpha1().SliceClaims(sliceCopy.Spec.ClaimRef.Namespace).Get(context.TODO(), sliceCopy.Spec.ClaimRef.Name, metav1.GetOptions{}); err != nil && errors.IsNotFound(err) {
			c.edgenetclientset.CoreV1alpha1().Slices().Delete(context.TODO(), sliceCopy.GetName(), metav1.DeleteOptions{})
		} else {
			if isReserved {
				c.syncWithSliceClaim(sliceCopy, sliceClaim)
			} else {
				sliceClaimCopy := sliceClaim.DeepCopy()
				sliceClaimCopy.Status.State = failure
				sliceClaimCopy.Status.Message = messageSliceFailed
				_, err := c.edgenetclientset.CoreV1alpha1().SliceClaims(sliceClaimCopy.GetNamespace()).UpdateStatus(context.TODO(), sliceClaimCopy, metav1.UpdateOptions{})
				klog.Infoln(err)
			}
		}
	}
}

func (c *Controller) syncWithSliceClaim(sliceCopy *corev1alpha1.Slice, sliceClaim *corev1alpha1.SliceClaim) {
	if ownerRef := metav1.GetControllerOf(sliceClaim); ownerRef == nil {
		sliceClaimCopy := sliceClaim.DeepCopy()
		sliceClaimCopy.SetOwnerReferences([]metav1.OwnerReference{sliceCopy.MakeOwnerReference()})
		_, err := c.edgenetclientset.CoreV1alpha1().SliceClaims(sliceClaimCopy.GetNamespace()).Update(context.TODO(), sliceClaimCopy, metav1.UpdateOptions{})
		klog.Infoln(err)
		return
	} else {
		if ownerRef.Kind == "Slice" {
			if ownerRef.UID != sliceCopy.GetUID() {
				c.edgenetclientset.CoreV1alpha1().Slices().Delete(context.TODO(), sliceCopy.GetName(), metav1.DeleteOptions{})
				return
			} else {
				if sliceClaim.Status.State == failure {
					c.edgenetclientset.CoreV1alpha1().Slices().Delete(context.TODO(), sliceCopy.GetName(), metav1.DeleteOptions{})
					return
				} else if sliceClaim.Status.State == applied {
					c.provisionSlice(sliceCopy)
				} else {
					c.recorder.Event(sliceCopy, corev1.EventTypeNormal, successBound, messageBound)
					sliceCopy.Status.State = bound
					sliceCopy.Status.Message = messageBound

					if sliceClaim.Status.State != bound {
						sliceClaimCopy := sliceClaim.DeepCopy()
						sliceClaimCopy.Status.State = bound
						sliceClaimCopy.Status.Message = messageBound
						_, err := c.edgenetclientset.CoreV1alpha1().SliceClaims(sliceClaimCopy.GetNamespace()).UpdateStatus(context.TODO(), sliceClaimCopy, metav1.UpdateOptions{})
						klog.Infoln(err)
					}
				}
			}
		}
	}
}

func (c *Controller) provisionSlice(sliceCopy *corev1alpha1.Slice) {
	if nodeRaw, err := c.kubeclientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("edge-net.io/pre-reservation=%s", sliceCopy.GetName())}); err == nil {
		for _, nodeRow := range nodeRaw.Items {
			c.patchNode("slice", sliceCopy.GetName(), nodeRow.GetName())
			if podRaw, err := c.kubeclientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeRow.GetName())}); err == nil {
				for _, podRow := range podRaw.Items {
					var zero int64 = 0
					c.kubeclientset.CoreV1().Pods(podRow.GetNamespace()).Delete(context.TODO(), podRow.GetName(), metav1.DeleteOptions{GracePeriodSeconds: &zero})
				}
			}
		}
		// c.recorder.Event(sliceCopy, corev1.EventTypeNormal, successBound, messagePr)
		sliceCopy.Status.State = provisioned
		// sliceCopy.Status.Message = messageReserved
	}
}

func (c *Controller) reserveNodes(sliceCopy *corev1alpha1.Slice) bool {
	if sliceCopy.Status.State != reserved && sliceCopy.Status.State != bound && sliceCopy.Status.State != provisioned {
		for _, nodeSelectorTerm := range sliceCopy.Spec.NodeSelector.Selector.NodeSelectorTerms {
			var labelSelector string
			var fieldSelector string
			for _, matchExpression := range nodeSelectorTerm.MatchExpressions {
				if labelSelector != "" {
					labelSelector = labelSelector + ","
				}
				if matchExpression.Operator == "In" || matchExpression.Operator == "NotIn" {
					labelSelector = fmt.Sprintf("%s%s %s (%s)", labelSelector, matchExpression.Key, strings.ToLower(string(matchExpression.Operator)), strings.Join(matchExpression.Values, ","))
				} else if matchExpression.Operator == "Exists" {
					labelSelector = fmt.Sprintf("%s%s", labelSelector, matchExpression.Key)
				} else if matchExpression.Operator == "DoesNotExist" {
					labelSelector = fmt.Sprintf("%s!%s", labelSelector, matchExpression.Key)
				} else {
					// TO-DO: Handle Gt and Lt operaters later.
					continue
				}
			}
			for _, matchField := range nodeSelectorTerm.MatchFields {
				if fieldSelector != "" {
					fieldSelector = fieldSelector + ","
				}
				if matchField.Operator == "In" || matchField.Operator == "NotIn" {
					fieldSelector = fmt.Sprintf("%s%s %s (%s)", fieldSelector, matchField.Key, strings.ToLower(string(matchField.Operator)), strings.Join(matchField.Values, ","))
				} else if matchField.Operator == "Exists" {
					fieldSelector = fmt.Sprintf("%s%s", fieldSelector, matchField.Key)
				} else if matchField.Operator == "DoesNotExist" {
					fieldSelector = fmt.Sprintf("%s!%s", fieldSelector, matchField.Key)
				} else {
					// TO-DO: Handle Gt and Lt operaters later.
					continue
				}
			}

			var nodeList []string
			if nodeRaw, err := c.kubeclientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: labelSelector, FieldSelector: fieldSelector}); err == nil {
				for _, nodeRow := range nodeRaw.Items {
					nodeLabels := nodeRow.GetLabels()
					if nodeLabels["edge-net.io/access"] == "private" || nodeLabels["edge-net.io/slice"] != "none" || nodeLabels["edge-net.io/pre-reservation"] != "none" {
						continue
					}

					match := false
					for key, value := range sliceCopy.Spec.NodeSelector.Resources.Limits {
						if value.Cmp(nodeRow.Status.Capacity[key]) == -1 {
							match = false
							break
						} else {
							match = true
						}
					}
					if match {
						for key, value := range sliceCopy.Spec.NodeSelector.Resources.Requests {
							if value.Cmp(nodeRow.Status.Capacity[key]) == 1 {
								match = false
								break
							} else {
								match = true
							}
						}
						if match {
							nodeList = append(nodeList, nodeRow.GetName())
						}
					}
				}
			}
			if len(nodeList) < sliceCopy.Spec.NodeSelector.Count {
				c.recorder.Event(sliceCopy, corev1.EventTypeWarning, failureSlice, messageSliceFailed)
				sliceCopy.Status.State = failure
				sliceCopy.Status.Message = messageSliceFailed
				return false
			} else {
				var pickedNodeList []string
				for i := 0; i < sliceCopy.Spec.NodeSelector.Count; i++ {
					rand.Seed(time.Now().UnixNano())
					randomSelect := rand.Intn(len(nodeList))
					pickedNodeList = append(pickedNodeList, nodeList[randomSelect])
					nodeList[randomSelect] = nodeList[len(nodeList)-1]
					nodeList = nodeList[:len(nodeList)-1]
				}
				isPatched := true
				for i := 0; i < len(pickedNodeList); i++ {
					if err := c.patchNode("reservation", sliceCopy.GetName(), pickedNodeList[i]); err != nil {
						c.recorder.Event(sliceCopy, corev1.EventTypeWarning, failurePatch, messagePatchFailed)
						sliceCopy.Status.State = failure
						sliceCopy.Status.Message = messagePatchFailed
						isPatched = false
						break
					}
				}
				if !isPatched {
					for i := 0; i < len(pickedNodeList); i++ {
						if err := c.patchNode("return", "", pickedNodeList[i]); err != nil {
							c.recorder.Event(sliceCopy, corev1.EventTypeWarning, failurePatch, messagePatchFailed)
						}
					}
					return false
				}
			}
		}
		c.recorder.Event(sliceCopy, corev1.EventTypeNormal, successReserved, messageReserved)
		sliceCopy.Status.State = reserved
		sliceCopy.Status.Message = messageReserved
	}
	return true
}

func (c *Controller) patchNode(kind, slice, node string) error {
	var err error
	type patchStringValue struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value string `json:"value"`
	}
	labels := make(map[string]string)
	if kind == "return" {
		labels["edge-net.io~1access"] = "public"
		labels["edge-net.io~1slice"] = "none"
		labels["edge-net.io~1pre-reservation"] = "none"
	} else if kind == "reservation" {
		labels["edge-net.io~1access"] = "public"
		labels["edge-net.io~1slice"] = "none"
		labels["edge-net.io~1pre-reservation"] = slice
	} else {
		labels["edge-net.io~1access"] = "private"
		labels["edge-net.io~1slice"] = slice
		labels["edge-net.io~1pre-reservation"] = slice
	}
	// Create a patch slice and initialize it to the label size
	patchArr := make([]patchStringValue, len(labels))
	patch := patchStringValue{}
	row := 0
	// Append the data existing in the label map to the slice
	for label, value := range labels {
		patch.Op = "add"
		patch.Path = fmt.Sprintf("/metadata/labels/%s", label)
		patch.Value = value
		patchArr[row] = patch
		row++
	}
	bytes, _ := json.Marshal(patchArr)

	_, err = c.kubeclientset.CoreV1().Nodes().Patch(context.TODO(), node, types.JSONPatchType, bytes, metav1.PatchOptions{})
	if err != nil {
		klog.Infoln(err.Error())
	}
	return err
}
