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

package rolerequest

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/EdgeNet-project/edgenet/pkg/access"
	registrationv1alpha1 "github.com/EdgeNet-project/edgenet/pkg/apis/registration/v1alpha1"
	clientset "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned"
	"github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/scheme"
	edgenetscheme "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned/scheme"
	informers "github.com/EdgeNet-project/edgenet/pkg/generated/informers/externalversions/registration/v1alpha1"
	listers "github.com/EdgeNet-project/edgenet/pkg/generated/listers/registration/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

const controllerAgentName = "rolerequest-controller"

// Definitions of the state of the rolerequest resource
const (
	successSynced          = "Synced"
	messageResourceSynced  = "Role Request synced successfully"
	successFound           = "Found"
	messageRoleFound       = "Requested Role / Cluster Role found successfully"
	failureFound           = "Not Found"
	messageRoleNotFound    = "Requested Role / Cluster Role does not exist"
	warningApproved        = "Not Approved"
	messageRoleNotApproved = "Waiting for Requested Role / Cluster Role to be approved"
	successApproved        = "Approved"
	messageRoleApproved    = "Requested Role / Cluster Role approved successfully"
	failureBinding         = "Binding Failed"
	messageBindingFailed   = "Role binding failed"
	failure                = "Failure"
	pending                = "Pending"
	approved               = "Approved"
)

// Controller is the controller implementation for Role Request resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// edgenetclientset is a clientset for the EdgeNet API groups
	edgenetclientset clientset.Interface

	rolerequestsLister listers.RoleRequestLister
	rolerequestsSynced cache.InformerSynced

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
	rolerequestInformer informers.RoleRequestInformer) *Controller {

	utilruntime.Must(edgenetscheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:      kubeclientset,
		edgenetclientset:   edgenetclientset,
		rolerequestsLister: rolerequestInformer.Lister(),
		rolerequestsSynced: rolerequestInformer.Informer().HasSynced,
		workqueue:          workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "RoleRequests"),
		recorder:           recorder,
	}

	klog.V(4).Infoln("Setting up event handlers")
	// Set up an event handler for when Role Request resources change
	rolerequestInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueRoleRequest,
		UpdateFunc: func(old, new interface{}) {
			newRoleRequest := new.(*registrationv1alpha1.RoleRequest)
			oldRoleRequest := old.(*registrationv1alpha1.RoleRequest)
			if (oldRoleRequest.Status.Expiry == nil && newRoleRequest.Status.Expiry != nil) ||
				!oldRoleRequest.Status.Expiry.Time.Equal(newRoleRequest.Status.Expiry.Time) {
				controller.enqueueRoleRequestAfter(newRoleRequest, time.Until(newRoleRequest.Status.Expiry.Time))
			}
			controller.enqueueRoleRequest(new)
		},
	})

	access.Clientset = kubeclientset
	access.EdgenetClientset = edgenetclientset

	return controller
}

// Run will set up the event handlers for the types of role request and node, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	klog.V(4).Infoln("Starting Role Request controller")

	klog.V(4).Infoln("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.rolerequestsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.V(4).Infoln("Starting workers")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.V(4).Infoln("Started workers")
	<-stopCh
	klog.V(4).Infoln("Shutting down workers")

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
		klog.V(4).Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Role Request
// resource with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	rolerequest, err := c.rolerequestsLister.RoleRequests(namespace).Get(name)

	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("rolerequest '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	if rolerequest.Status.State != approved {
		c.processRoleRequest(rolerequest.DeepCopy())
	}
	c.recorder.Event(rolerequest, corev1.EventTypeNormal, successSynced, messageResourceSynced)
	return nil
}

// enqueueRoleRequest takes a RoleRequest resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than RoleRequest.
func (c *Controller) enqueueRoleRequest(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// enqueueRoleRequestAfter takes a RoleRequest resource and converts it into a namespace/name
// string which is then put onto the work queue after the expiry date to be deleted. This method should *not* be
// passed resources of any type other than RoleRequest.
func (c *Controller) enqueueRoleRequestAfter(obj interface{}, after time.Duration) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.AddAfter(key, after)
}

func (c *Controller) processRoleRequest(roleRequestCopy *registrationv1alpha1.RoleRequest) {
	oldStatus := roleRequestCopy.Status
	statusUpdate := func() {
		if !reflect.DeepEqual(oldStatus, roleRequestCopy.Status) {
			if _, err := c.edgenetclientset.RegistrationV1alpha1().RoleRequests(roleRequestCopy.GetNamespace()).UpdateStatus(context.TODO(), roleRequestCopy, metav1.UpdateOptions{}); err != nil {
				klog.V(4).Infoln(err)
			}
		}
	}
	if roleRequestCopy.Status.Expiry == nil {
		// Set the approval timeout which is 72 hours
		roleRequestCopy.Status.Expiry = &metav1.Time{
			Time: time.Now().Add(72 * time.Hour),
		}
	} else if time.Until(roleRequestCopy.Status.Expiry.Time) <= 0 {
		c.edgenetclientset.RegistrationV1alpha1().RoleRequests(roleRequestCopy.GetNamespace()).Delete(context.TODO(), roleRequestCopy.GetName(), metav1.DeleteOptions{})
		return
	}
	defer statusUpdate()

	// Below code checks whether namespace, where role request made, is local to the cluster or is propagated along with a federated deployment.
	// If another cluster propagates the namespace, we skip checking the owner tenant's status as the Selective Deployment entity manages this life-cycle.
	permitted := false
	systemNamespace, err := c.kubeclientset.CoreV1().Namespaces().Get(context.TODO(), "kube-system", metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infoln(err)
		c.edgenetclientset.RegistrationV1alpha1().RoleRequests(roleRequestCopy.GetNamespace()).Delete(context.TODO(), roleRequestCopy.GetName(), metav1.DeleteOptions{})
		return
	}
	namespace, err := c.kubeclientset.CoreV1().Namespaces().Get(context.TODO(), roleRequestCopy.GetNamespace(), metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infoln(err)
		c.edgenetclientset.RegistrationV1alpha1().RoleRequests(roleRequestCopy.GetNamespace()).Delete(context.TODO(), roleRequestCopy.GetName(), metav1.DeleteOptions{})
		return
	}
	namespaceLabels := namespace.GetLabels()
	if systemNamespace.GetUID() != types.UID(namespaceLabels["edge-net.io/cluster-uid"]) {
		permitted = true
	} else {
		tenant, err := c.edgenetclientset.CoreV1alpha1().Tenants().Get(context.TODO(), strings.ToLower(namespaceLabels["edge-net.io/tenant"]), metav1.GetOptions{})
		if err != nil {
			klog.V(4).Infoln(err)
			c.edgenetclientset.RegistrationV1alpha1().RoleRequests(roleRequestCopy.GetNamespace()).Delete(context.TODO(), roleRequestCopy.GetName(), metav1.DeleteOptions{})
			return
		}
		if tenant.GetUID() == types.UID(namespaceLabels["edge-net.io/tenant-uid"]) && tenant.Spec.Enabled {
			permitted = true
		}
	}

	if permitted {
		// Below is to ensure that the requested Role / ClusterRole exists before moving forward in the procedure.
		// If not, the status of the object falls into an error state.
		roleExists := c.checkForRequestedRole(roleRequestCopy)
		if !roleExists {
			return
		}

		if !roleRequestCopy.Spec.Approved {
			if roleRequestCopy.Status.State == pending && roleRequestCopy.Status.Message == messageRoleNotApproved {
				return
			}
			c.recorder.Event(roleRequestCopy, corev1.EventTypeWarning, warningApproved, messageRoleNotApproved)
			roleRequestCopy.Status.State = pending
			roleRequestCopy.Status.Message = messageRoleNotApproved
		} else {
			c.recorder.Event(roleRequestCopy, corev1.EventTypeNormal, successApproved, messageRoleApproved)
			roleRequestCopy.Status.State = approved
			roleRequestCopy.Status.Message = messageRoleApproved

			// The following section handles role binding. There are two basic logical steps here.
			// Check if role binding already exists; if not, create a role binding for the user.
			// If role binding exists, check if the user already holds the role. If not, pin the role to the user.
			if roleBindingRaw, err := c.kubeclientset.RbacV1().RoleBindings(roleRequestCopy.GetNamespace()).List(context.TODO(), metav1.ListOptions{LabelSelector: "edge-net.io/generated=true"}); err == nil {
				// TODO: Simplfy below
				roleBindingExists := false
				roleBound := false
				for _, roleBindingRow := range roleBindingRaw.Items {
					if roleBindingRow.GetName() == roleRequestCopy.Spec.RoleRef.Name && roleBindingRow.RoleRef.Name == roleRequestCopy.Spec.RoleRef.Name && roleBindingRow.RoleRef.Kind == roleRequestCopy.Spec.RoleRef.Kind {
						roleBindingExists = true
						for _, subjectRow := range roleBindingRow.Subjects {
							if subjectRow.Kind == "User" && subjectRow.Name == roleRequestCopy.Spec.Email {
								break
							}
						}
						if !roleBound {
							roleBindingCopy := roleBindingRow.DeepCopy()
							roleBindingCopy.Subjects = append(roleBindingCopy.Subjects, rbacv1.Subject{Kind: "User", Name: roleRequestCopy.Spec.Email, APIGroup: "rbac.authorization.k8s.io"})
							if _, err := c.kubeclientset.RbacV1().RoleBindings(roleBindingCopy.GetNamespace()).Update(context.TODO(), roleBindingCopy, metav1.UpdateOptions{}); err != nil {
								c.recorder.Event(roleRequestCopy, corev1.EventTypeWarning, failureBinding, messageBindingFailed)
								roleRequestCopy.Status.State = failure
								roleRequestCopy.Status.Message = messageBindingFailed
								klog.V(4).Infoln(err)
							}
							break
						}
					}
				}
				if !roleBindingExists {
					roleRef := rbacv1.RoleRef{Kind: roleRequestCopy.Spec.RoleRef.Kind, Name: roleRequestCopy.Spec.RoleRef.Name}
					rbSubjects := []rbacv1.Subject{{Kind: "User", Name: roleRequestCopy.Spec.Email, APIGroup: "rbac.authorization.k8s.io"}}
					roleBind := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleRequestCopy.Spec.RoleRef.Name, Namespace: roleRequestCopy.GetNamespace()},
						Subjects: rbSubjects, RoleRef: roleRef}
					roleBindLabels := map[string]string{"edge-net.io/generated": "true"}
					roleBind.SetLabels(roleBindLabels)
					if _, err := c.kubeclientset.RbacV1().RoleBindings(roleRequestCopy.GetNamespace()).Create(context.TODO(), roleBind, metav1.CreateOptions{}); err != nil {
						c.recorder.Event(roleRequestCopy, corev1.EventTypeWarning, failureBinding, messageBindingFailed)
						roleRequestCopy.Status.State = failure
						roleRequestCopy.Status.Message = messageBindingFailed
						klog.V(4).Infoln(err)
					}
				}
			}
		}
	} else {
		c.edgenetclientset.RegistrationV1alpha1().RoleRequests(roleRequestCopy.GetNamespace()).Delete(context.TODO(), roleRequestCopy.GetName(), metav1.DeleteOptions{})
	}
}

func (c *Controller) checkForRequestedRole(roleRequestCopy *registrationv1alpha1.RoleRequest) bool {
	if roleRequestCopy.Spec.RoleRef.Kind == "ClusterRole" {
		if clusterRoleRaw, err := c.kubeclientset.RbacV1().ClusterRoles().List(context.TODO(), metav1.ListOptions{}); err == nil {
			for _, clusterRoleRow := range clusterRoleRaw.Items {
				if clusterRoleRow.GetName() == roleRequestCopy.Spec.RoleRef.Name {
					c.recorder.Event(roleRequestCopy, corev1.EventTypeNormal, successFound, messageRoleFound)
					return true
				}
			}
		}
	} else if roleRequestCopy.Spec.RoleRef.Kind == "Role" {
		if roleRaw, err := c.kubeclientset.RbacV1().Roles(roleRequestCopy.GetNamespace()).List(context.TODO(), metav1.ListOptions{}); err == nil {
			for _, roleRow := range roleRaw.Items {
				if roleRow.GetName() == roleRequestCopy.Spec.RoleRef.Name {
					c.recorder.Event(roleRequestCopy, corev1.EventTypeNormal, successFound, messageRoleFound)
					return true
				}
			}
		}
	}

	c.recorder.Event(roleRequestCopy, corev1.EventTypeWarning, failureFound, messageRoleNotFound)
	roleRequestCopy.Status.State = failure
	roleRequestCopy.Status.Message = messageRoleNotFound
	return false
}
