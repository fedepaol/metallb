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
	"go.universe.tf/metallb/internal/k8s/epslices"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConfigMap Reconciler reconciles a Peer object
type ServiceReconciler struct {
	client.Client
	Log         log.Logger
	Scheme      *runtime.Scheme
	Namespace   string
	Handler     func(log.Logger, string, *v1.Service, epslices.EpsOrSlices) SyncState
	UseEpSlices bool // TODO CRD Epslices
}

// TODO CRD -> differentiate between controller and speaker
//+kubebuilder:rbac:groups="",resources=service,verbs=get;list;watch;
//+kubebuilder:rbac:groups="",resources=service/status,verbs=update;

func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	//r.Log.Info(fmt.Sprintf("Starting ConfigMap reconcile loop for %v", req.NamespacedName))
	//defer r.Log.Info(fmt.Sprintf("Finish ConfigMap reconcile loop for %v", req.NamespacedName))

	var service v1.Service
	if err := r.Get(ctx, req.NamespacedName, &service); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var epSlices discovery.EndpointSliceList
	if err := r.List(ctx, &epSlices, client.InNamespace(req.Namespace), client.MatchingFields{epslices.SlicesServiceIndexName: req.NamespacedName.String()}); err != nil {
		// log.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}

	res := r.Handler(r.Log, service.Name, &service, epslices.EpsOrSlices{SlicesVal: epSlices.Items, Type: epslices.Slices})
	switch res {
	// The update caused a transient error, the k8s client should
	// retry later.
	case SyncStateError:
		return ctrl.Result{RequeueAfter: RetryPeriod}, nil
	case SyncStateReprocessAll:
		// TODO CRD resync services
	case SyncStateErrorNoRetry:
		return ctrl.Result{}, nil
	}
	// err := reconcileConfigMap(ctx, r.Client, r.Log, r.Namespace)
	// if errors.As(err, &render.RenderingFailed{}) {
	// 	r.Log.Error(err, "configmap rendering failed", "controller", "bgppeer")
	// 	return ctrl.Result{}, nil
	// }
	// if err != nil {
	// 	r.Log.Error(err, "failed to reconcile configmap", "controller", "bgppeer")
	// 	return ctrl.Result{RequeueAfter: RetryPeriod}, err
	// }
	return ctrl.Result{}, nil
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Service{}).
		Complete(r)
}
