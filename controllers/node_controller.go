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
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ConfigMap Reconciler reconciles a Peer object
type NodeReconciler struct {
	client.Client
	Log       log.Logger
	Scheme    *runtime.Scheme
	Namespace string
	Handler   func(log.Logger, *v1.Node) SyncState
}

//+kubebuilder:rbac:groups=corev1,resources=node,verbs=get;list;watch;

func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	//r.Log.Info(fmt.Sprintf("Starting ConfigMap reconcile loop for %v", req.NamespacedName))
	//defer r.Log.Info(fmt.Sprintf("Finish ConfigMap reconcile loop for %v", req.NamespacedName))

	res := r.Handler(r.Log, nil)
	switch res {
	// The update caused a transient error, the k8s client should
	// retry later.
	case SyncStateError:
		return ctrl.Result{RequeueAfter: RetryPeriod}, nil
	case SyncStateReprocessAll:
		// TODO resync services
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

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Service{}).
		Watches(&source.Kind{Type: &v1.Endpoints{}}, &handler.EnqueueRequestForObject{}).
		Watches(&source.Kind{Type: &discovery.EndpointSlice{}}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
