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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ServiceReloadReconciler force reloading services triggered via a channel.
type ServiceReloadReconciler struct {
	client.Client
	Log       log.Logger
	Scheme    *runtime.Scheme
	Endpoints NeedEndPoints
	Handler   func(log.Logger, string, *v1.Service, epslices.EpsOrSlices) SyncState
	Reload    chan event.GenericEvent
}

func (r *ServiceReloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	level.Info(r.Log).Log("controller", "ServiceReloadReconciler", "start reconcile", req.NamespacedName.String())
	defer level.Info(r.Log).Log("controller", "ServiceReloadReconciler", "end reconcile", req.NamespacedName.String())

	return r.reprocessAllServices(ctx)
}

func (r *ServiceReloadReconciler) reprocessAllServices(ctx context.Context) (ctrl.Result, error) {
	var services v1.ServiceList
	if err := r.List(ctx, &services); err != nil {
		level.Error(r.Log).Log("controller", "ServiceReloadReconciler", "error", "failed to list the services", "error", err)
		return ctrl.Result{RequeueAfter: RetryPeriod}, nil
	}

	for _, service := range services.Items {

		serviceName := types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
		eps, err := epSlicesForServices(ctx, r, serviceName, r.Endpoints)
		if err != nil {
			level.Error(r.Log).Log("controller", "ServiceReconciler", "error", "failed to get endpoints", "service", serviceName.String(), "error", err)
			return ctrl.Result{}, err
		}

		res := r.Handler(r.Log, serviceName.String(), &service, eps)
		switch res {
		case SyncStateError:
			return ctrl.Result{RequeueAfter: RetryPeriod}, nil
		case SyncStateReprocessAll:
			r.Reload <- event.GenericEvent{}
		case SyncStateErrorNoRetry:
			return ctrl.Result{}, nil
		}
	}
	return ctrl.Result{}, nil
}

func (r *ServiceReloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := controller.New("ServiceReloadReconciler", mgr,
		controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Channel{Source: r.Reload}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	go func() {
		err := c.Start(ctrl.SetupSignalHandler())
		if err != nil {
			level.Error(r.Log).Log("controller", "ServiceReloadReconciler", "failed to start", err)
		}
	}()
	return nil
}
