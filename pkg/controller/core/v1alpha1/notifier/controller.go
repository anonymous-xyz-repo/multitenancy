/*
Copyright 2022 Contributors to the EdgeNet project.

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

// TODO: This entity should be implemented by a CRD where notification medium and events can be declared.
package notifier

import (
	"context"
	"fmt"
	"net/mail"
	"reflect"
	"regexp"
	"time"

	"github.com/EdgeNet-project/edgenet/pkg/access"
	registrationv1alpha1 "github.com/EdgeNet-project/edgenet/pkg/apis/registration/v1alpha1"
	clientset "github.com/EdgeNet-project/edgenet/pkg/generated/clientset/versioned"
	informers "github.com/EdgeNet-project/edgenet/pkg/generated/informers/externalversions/registration/v1alpha1"
	listers "github.com/EdgeNet-project/edgenet/pkg/generated/listers/registration/v1alpha1"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	scheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const controllerAgentName = "notifier-controller"

// Definitions of the state of the tenantrequest resource
const (
	failure = "Failure"
	pending = "Pending"
)

// The main structure of controller
type Controller struct {
	kubeclientset    kubernetes.Interface
	edgenetclientset clientset.Interface

	tenantrequestsLister      listers.TenantRequestLister
	tenantrequestsSynced      cache.InformerSynced
	rolerequestsLister        listers.RoleRequestLister
	rolerequestsSynced        cache.InformerSynced
	clusterrolerequestsLister listers.ClusterRoleRequestLister
	clusterrolerequestsSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueueTenantRequest      workqueue.RateLimitingInterface
	workqueueClusterRoleRequest workqueue.RateLimitingInterface
	workqueueRoleRequest        workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new controller
func NewController(
	kubeclientset kubernetes.Interface,
	edgenetclientset clientset.Interface,
	tenantrequestInformer informers.TenantRequestInformer,
	rolerequestInformer informers.RoleRequestInformer,
	clusterrolerequestInformer informers.ClusterRoleRequestInformer) *Controller {
	// Create event broadcaster
	utilruntime.Must(scheme.AddToScheme(scheme.Scheme))
	klog.Infoln("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:               kubeclientset,
		edgenetclientset:            edgenetclientset,
		tenantrequestsLister:        tenantrequestInformer.Lister(),
		tenantrequestsSynced:        tenantrequestInformer.Informer().HasSynced,
		rolerequestsLister:          rolerequestInformer.Lister(),
		rolerequestsSynced:          rolerequestInformer.Informer().HasSynced,
		clusterrolerequestsLister:   clusterrolerequestInformer.Lister(),
		clusterrolerequestsSynced:   clusterrolerequestInformer.Informer().HasSynced,
		workqueueTenantRequest:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "NotifierTenantRequest"),
		workqueueClusterRoleRequest: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "NotifierClusterRoleRequest"),
		workqueueRoleRequest:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "NotifierRoleRequest"),
		recorder:                    recorder,
	}
	klog.Infoln("Setting up event handlers")

	// Event handlers deal with events of resources.
	tenantrequestInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			newTenantRequest := new.(*registrationv1alpha1.TenantRequest)
			oldTenantRequest := old.(*registrationv1alpha1.TenantRequest)
			if !reflect.DeepEqual(newTenantRequest.Status, oldTenantRequest.Status) {
				controller.enqueueNotifier(new)
			}
		},
	})
	rolerequestInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			newRoleRequest := new.(*registrationv1alpha1.RoleRequest)
			oldRoleRequest := old.(*registrationv1alpha1.RoleRequest)
			if !reflect.DeepEqual(newRoleRequest.Status, oldRoleRequest.Status) {
				controller.enqueueNotifier(new)
			}
		},
	})
	clusterrolerequestInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			newClusterRoleRequest := new.(*registrationv1alpha1.ClusterRoleRequest)
			oldClusterRoleRequest := old.(*registrationv1alpha1.ClusterRoleRequest)
			if !reflect.DeepEqual(newClusterRoleRequest.Status, oldClusterRoleRequest.Status) {
				controller.enqueueNotifier(new)
			}
		},
	})

	return controller
}

func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueueTenantRequest.ShutDown()
	defer c.workqueueClusterRoleRequest.ShutDown()
	defer c.workqueueRoleRequest.ShutDown()

	klog.Infoln("Starting Notifier Controller")

	klog.Infoln("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(stopCh,
		c.tenantrequestsSynced,
		c.rolerequestsSynced,
		c.clusterrolerequestsSynced); !ok {
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

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	isSyncedTenant := c.processNextTenantRequestItem()
	isSyncedClusterRoleRequest := c.processNextClusterRoleRequestItem()
	isSyncedRoleRequest := c.processNextRoleRequestItem()

	if !isSyncedTenant && !isSyncedClusterRoleRequest && !isSyncedRoleRequest {
		return false
	}
	return true
}

func (c *Controller) processNextTenantRequestItem() bool {
	obj, shutdown := c.workqueueTenantRequest.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueueTenantRequest.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.workqueueTenantRequest.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		if err := c.syncTenantRequestHandler(key); err != nil {
			c.workqueueTenantRequest.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}

		c.workqueueTenantRequest.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) processNextClusterRoleRequestItem() bool {
	obj, shutdown := c.workqueueClusterRoleRequest.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueueClusterRoleRequest.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.workqueueClusterRoleRequest.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		if err := c.syncClusterRoleRequestHandler(key); err != nil {
			c.workqueueClusterRoleRequest.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}

		c.workqueueClusterRoleRequest.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) processNextRoleRequestItem() bool {
	obj, shutdown := c.workqueueRoleRequest.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueueRoleRequest.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.workqueueRoleRequest.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		if err := c.syncRoleRequestHandler(key); err != nil {
			c.workqueueRoleRequest.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}

		c.workqueueRoleRequest.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncTenantRequestHandler looks at the actual state and sends a notification if desired.
func (c *Controller) syncTenantRequestHandler(key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	tenantrequest, err := c.tenantrequestsLister.Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("tenant request '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}
	klog.Infof("processNextTenantRequestItem: object created/updated detected: %s", key)
	c.processTenantRequest(tenantrequest)

	return nil
}

// syncClusterRoleRequestHandler looks at the actual state and sends a notification if desired.
func (c *Controller) syncClusterRoleRequestHandler(key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	clusterrolerequest, err := c.clusterrolerequestsLister.Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("cluster role request '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}
	klog.Infof("processNextClusterRoleRequestItem: object created/updated detected: %s", key)
	c.processClusterRoleRequest(clusterrolerequest)

	return nil
}

// syncRoleRequestHandler looks at the actual state and sends a notification if desired.
func (c *Controller) syncRoleRequestHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	rolerequest, err := c.rolerequestsLister.RoleRequests(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("role request '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}
	klog.Infof("processNextRoleRequestItem: object created/updated detected: %s", key)
	c.processRoleRequest(rolerequest)

	return nil
}

func (c *Controller) enqueueNotifier(obj interface{}) {
	// Put the resource object into a key
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.Infoln(obj)
	switch obj.(type) {
	case *registrationv1alpha1.TenantRequest:
		c.workqueueTenantRequest.Add(key)
	case *registrationv1alpha1.ClusterRoleRequest:
		c.workqueueClusterRoleRequest.Add(key)
	case *registrationv1alpha1.RoleRequest:
		c.workqueueRoleRequest.Add(key)
	}
}

func (c *Controller) processTenantRequest(tenantrequest *registrationv1alpha1.TenantRequest) {
	klog.Infoln("processTenantRequest")
	//nodeObj := obj.(*corev1.Node)

	systemNamespace, err := c.kubeclientset.CoreV1().Namespaces().Get(context.TODO(), "kube-system", metav1.GetOptions{})
	if err != nil {
		return
	}
	if tenantrequest.Status.State == failure || tenantrequest.Status.State == "" {
		return
	} else if tenantrequest.Status.State == pending {
		// The function below notifies those who have the right to approve this tenant request.
		// As tenant requests are cluster-wide resources, we check the permissions granted by Cluster Role Binding following a pattern to avoid overhead.
		// Furthermore, only those to which the system has granted permission, by attaching the "edge-net.io/generated=true" label, receive a notification email.
		emailList := []string{}
		if clusterRoleBindingRaw, err := c.kubeclientset.RbacV1().ClusterRoleBindings().List(context.TODO(), metav1.ListOptions{LabelSelector: "edge-net.io/generated=true"}); err == nil {
			r, _ := regexp.Compile("(.*)(edgenet:clusteradministration)(.*)(admin|manager|deputy)(.*)")
			for _, clusterRoleBindingRow := range clusterRoleBindingRaw.Items {
				if match := r.MatchString(clusterRoleBindingRow.GetName()); !match {
					continue
				}
				for _, subjectRow := range clusterRoleBindingRow.Subjects {
					if subjectRow.Kind == "User" {
						_, err := mail.ParseAddress(subjectRow.Name)
						if err == nil {
							subjectAccessReview := new(authorizationv1.SubjectAccessReview)
							resourceAttributes := new(authorizationv1.ResourceAttributes)
							resourceAttributes.Group = "registration.edgenet.io"
							resourceAttributes.Version = "v1alpha1"
							resourceAttributes.Resource = "tenantrequests"
							resourceAttributes.Verb = "UPDATE"
							resourceAttributes.Name = tenantrequest.GetName()
							subjectAccessReview.Spec.ResourceAttributes = resourceAttributes
							subjectAccessReview.Spec.User = subjectRow.Name
							if subjectAccessReviewResult, err := c.kubeclientset.AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), subjectAccessReview, metav1.CreateOptions{}); err == nil {
								if subjectAccessReviewResult.Status.Allowed {
									emailList = append(emailList, subjectRow.Name)
								}
							}
						}
					}
				}
			}
		}
		if len(emailList) > 0 {
			klog.Infoln(emailList)
			access.SendEmailForTenantRequest(tenantrequest, "tenant-request-made", "[EdgeNet Admin] A tenant request made",
				string(systemNamespace.GetUID()), emailList)
			access.SendSlackNotificationForTenantRequest(tenantrequest, "tenant-request-made", "[EdgeNet Admin] A tenant request made",
				string(systemNamespace.GetUID()))
		}
	} else {
		access.SendEmailForTenantRequest(tenantrequest, "tenant-request-approved", "[EdgeNet] Tenant request approved",
			string(systemNamespace.GetUID()), []string{tenantrequest.Spec.Contact.Email})
		access.SendSlackNotificationForTenantRequest(tenantrequest, "tenant-request-approved", "[EdgeNet] Tenant request approved",
			string(systemNamespace.GetUID()))
	}
}

func (c *Controller) processRoleRequest(rolerequest *registrationv1alpha1.RoleRequest) {
	klog.Infoln("processRoleRequest")

	systemNamespace, err := c.kubeclientset.CoreV1().Namespaces().Get(context.TODO(), "kube-system", metav1.GetOptions{})
	if err != nil {
		return
	}
	if rolerequest.Status.State == failure || rolerequest.Status.State == "" {
		return
	} else if rolerequest.Status.State == pending {
		// The function below notifies those who have the right to approve this role request.
		// As role requests run on the layer of namespaces, we here ignore the permissions granted by Cluster Role Binding to avoid email floods.
		// Furthermore, only those to which the system has granted permission, by attaching the "edge-net.io/generated=true" label, receive a notification email.
		emailList := []string{}
		if roleBindingRaw, err := c.kubeclientset.RbacV1().RoleBindings(rolerequest.GetNamespace()).List(context.TODO(), metav1.ListOptions{LabelSelector: "edge-net.io/generated=true"}); err == nil {
			r, _ := regexp.Compile("(.*)(owner|admin|manager|deputy)(.*)")
			for _, roleBindingRow := range roleBindingRaw.Items {
				if match := r.MatchString(roleBindingRow.GetName()); !match {
					continue
				}
				for _, subjectRow := range roleBindingRow.Subjects {
					if subjectRow.Kind == "User" {
						_, err := mail.ParseAddress(subjectRow.Name)
						if err == nil {
							subjectAccessReview := new(authorizationv1.SubjectAccessReview)
							resourceAttributes := new(authorizationv1.ResourceAttributes)
							resourceAttributes.Group = "registration.edgenet.io"
							resourceAttributes.Version = "v1alpha1"
							resourceAttributes.Resource = "rolerequests"
							resourceAttributes.Verb = "UPDATE"
							resourceAttributes.Namespace = rolerequest.GetNamespace()
							resourceAttributes.Name = rolerequest.GetName()
							subjectAccessReview.Spec.ResourceAttributes = resourceAttributes
							subjectAccessReview.Spec.User = subjectRow.Name
							if subjectAccessReviewResult, err := c.kubeclientset.AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), subjectAccessReview, metav1.CreateOptions{}); err == nil {
								if subjectAccessReviewResult.Status.Allowed {
									emailList = append(emailList, subjectRow.Name)
								}
							}
						}
					}
				}
			}
		}
		if len(emailList) > 0 {
			access.SendEmailForRoleRequest(rolerequest, "role-request-made", "[EdgeNet] A role request made",
				string(systemNamespace.GetUID()), emailList)
			access.SendSlackNotificationForRoleRequest(rolerequest, "role-request-made", "[EdgeNet] A role request made",
				string(systemNamespace.GetUID()))
		}
	} else {
		access.SendEmailForRoleRequest(rolerequest, "role-request-approved", "[EdgeNet] Role request approved",
			string(systemNamespace.GetUID()), []string{rolerequest.Spec.Email})
		access.SendSlackNotificationForRoleRequest(rolerequest, "role-request-approved", "[EdgeNet] Role request approved",
			string(systemNamespace.GetUID()))
	}
}

func (c *Controller) processClusterRoleRequest(clusterRolerequest *registrationv1alpha1.ClusterRoleRequest) {
	klog.Infoln("processClusterRoleRequest")

	systemNamespace, err := c.kubeclientset.CoreV1().Namespaces().Get(context.TODO(), "kube-system", metav1.GetOptions{})
	if err != nil {
		return
	}
	if clusterRolerequest.Status.State == failure || clusterRolerequest.Status.State == "" {
		return
	} else if clusterRolerequest.Status.State == pending {
		// The function below notifies those who have the right to approve this role request.
		// As role requests run on the layer of namespaces, we here ignore the permissions granted by Cluster Role Binding to avoid email floods.
		// Furthermore, only those to which the system has granted permission, by attaching the "edge-net.io/generated=true" label, receive a notification email.
		emailList := []string{}
		if roleBindingRaw, err := c.kubeclientset.RbacV1().ClusterRoleBindings().List(context.TODO(), metav1.ListOptions{LabelSelector: "edge-net.io/generated=true"}); err == nil {
			r, _ := regexp.Compile("(.*)(owner|admin|manager|deputy)(.*)")
			for _, roleBindingRow := range roleBindingRaw.Items {
				if match := r.MatchString(roleBindingRow.GetName()); !match {
					continue
				}
				for _, subjectRow := range roleBindingRow.Subjects {
					if subjectRow.Kind == "User" {
						_, err := mail.ParseAddress(subjectRow.Name)
						if err == nil {
							subjectAccessReview := new(authorizationv1.SubjectAccessReview)
							resourceAttributes := new(authorizationv1.ResourceAttributes)
							resourceAttributes.Group = "registration.edgenet.io"
							resourceAttributes.Version = "v1alpha1"
							resourceAttributes.Resource = "clusterrolerequests"
							resourceAttributes.Verb = "UPDATE"
							resourceAttributes.Name = clusterRolerequest.GetName()
							subjectAccessReview.Spec.ResourceAttributes = resourceAttributes
							subjectAccessReview.Spec.User = subjectRow.Name
							if subjectAccessReviewResult, err := c.kubeclientset.AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), subjectAccessReview, metav1.CreateOptions{}); err == nil {
								if subjectAccessReviewResult.Status.Allowed {
									emailList = append(emailList, subjectRow.Name)
								}
							}
						}
					}
				}
			}
		}
		if len(emailList) > 0 {
			access.SendEmailForClusterRoleRequest(clusterRolerequest, "clusterrole-request-made", "[EdgeNet] A cluster role request made",
				string(systemNamespace.GetUID()), emailList)
			access.SendSlackNotificationForClusterRoleRequest(clusterRolerequest, "clusterrole-request-made", "[EdgeNet] A cluster role request made",
				string(systemNamespace.GetUID()))
		}
	} else {
		access.SendEmailForClusterRoleRequest(clusterRolerequest, "clusterrole-request-approved", "[EdgeNet] Cluster role request approved",
			string(systemNamespace.GetUID()), []string{clusterRolerequest.Spec.Email})
		access.SendSlackNotificationForClusterRoleRequest(clusterRolerequest, "clusterrole-request-approved", "[EdgeNet] Cluster role request approved",
			string(systemNamespace.GetUID()))
	}
}
