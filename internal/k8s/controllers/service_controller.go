/*


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

package controllers

import (
	"context"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/log/level"
	"go.universe.tf/metallb/internal/k8s/epslices"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ConfigMap Reconciler reconciles a Peer object
type ServiceReconciler struct {
	client.Client
	Log         log.Logger
	Scheme      *runtime.Scheme
	Namespace   string
	Handler     func(log.Logger, string, *v1.Service, epslices.EpsOrSlices) SyncState
	Endpoints   NeedEndPoints
	ForceReload func()
}

//+kubebuilder:rbac:groups="",resources=service,verbs=get;list;watch;
//+kubebuilder:rbac:groups="",resources=service/status,verbs=update;
//+kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch;
//+kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch;

func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	level.Info(r.Log).Log("controller", "ServiceReconciler", "start reconcile", req.NamespacedName.String())
	defer level.Info(r.Log).Log("controller", "ServiceReconciler", "end reconcile", req.NamespacedName.String())

	var service *v1.Service

	var toGet v1.Service
	err := r.Get(ctx, req.NamespacedName, &toGet)
	if !apierrors.IsNotFound(err) { // in case of delete, we need to pass nil to the handler
		service = &toGet
	}
	if err != nil && !apierrors.IsNotFound(err) {
		level.Error(r.Log).Log("controller", "ServiceReconciler", "error", "failed to get service", "service", req.NamespacedName, "error", err)
		return ctrl.Result{}, nil
	}

	epSlices, err := epSlicesForServices(ctx, r, req.NamespacedName, r.Endpoints)
	if err != nil {
		level.Error(r.Log).Log("controller", "ServiceReconciler", "error", "failed to get endpoints", "service", req.NamespacedName, "error", err)
		return ctrl.Result{}, err
	}
	res := r.Handler(r.Log, req.NamespacedName.String(), service, epSlices)
	switch res {
	case SyncStateError:
		return ctrl.Result{RequeueAfter: RetryPeriod}, nil
	case SyncStateReprocessAll:
		level.Info(r.Log).Log("controller", "ServiceReconciler", "event", "force service reload")
		r.ForceReload()
		return ctrl.Result{}, nil
	case SyncStateErrorNoRetry:
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Endpoints == EndpointSlices {
		return ctrl.NewControllerManagedBy(mgr).
			For(&v1.Service{}).
			Watches(&source.Kind{Type: &discovery.EndpointSlice{}},
				handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
					epSlice, ok := obj.(*discovery.EndpointSlice)
					if !ok {
						level.Error(r.Log).Log("controller", "ServiceReconciler", "error", "received an object that is not epslice")
						return []reconcile.Request{}
					}
					serviceName, err := epslices.ServiceKeyForSlice(epSlice)
					if err != nil {
						level.Error(r.Log).Log("controller", "ServiceReconciler", "error", "failed to get serviceName for slice", "error", err, "epslice", epSlice.Name)
						return []reconcile.Request{}
					}
					return []reconcile.Request{{NamespacedName: serviceName}}
				})).
			Complete(r)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Service{}).
		Complete(r)
}
