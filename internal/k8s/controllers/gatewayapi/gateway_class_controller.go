package gatewayapi

import (
	"context"
	"log"

	"github.com/go-kit/log/level"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/gateway-api/apis/v1beta1"
)

type GatewayClassReconciler struct {
	client.Client
	Logger         log.Logger
	Scheme         *runtime.Scheme
	ControllerName string
}

func (r *GatewayClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	level.Info(r.Logger).Log("controller", "NodeReconciler", "start reconcile", req.NamespacedName.String())
	defer level.Info(r.Logger).Log("controller", "NodeReconciler", "end reconcile", req.NamespacedName.String())

	var gatewayClass v1beta1.GatewayClass
	err := r.Get(ctx, req.NamespacedName, &gatewayClass)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if gatewayClass.Spec.ControllerName != v1beta1.GatewayController(r.ControllerName) {
		return ctrl.Result{}, nil
	}
	for i, c := range gatewayClass.Status.Conditions {
		if c.Type == "Accepted" {
			c.Status = "True"
			c.LastTransitionTime = metav1.Now()
		}
		gatewayClass.Status.Conditions[i] = c
	}
	err = r.Client.Status().Update(ctx, &gatewayClass, &client.SubResourceUpdateOptions{})
	if err != nil {
		level.Error(r.Logger).Log("Error", err, "message", "unable to update gateway class")
	}

	return ctrl.Result{}, nil
}

func (r *GatewayClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.GatewayClass{}).
		WithEventFilter(predicate.NewPredicateFuncs(
			func(object client.Object) bool {
				gatewayClass, ok := object.(*v1beta1.GatewayClass)
				if !ok {
					return false
				}
				if gatewayClass.Spec.Controller == r.ControllerName {
					return true
				}
				return false
			})).
		Complete(r)
}
