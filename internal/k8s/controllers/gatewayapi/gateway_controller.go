package gatewayapi

import (
	"context"
	"log"

	"github.com/go-kit/log/level"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/gateway-api/apis/v1beta1"
)

type GatewayReconciler struct {
	client.Client
	Logger         log.Logger
	Scheme         *runtime.Scheme
	ControllerName string
	Namespace      string
}

func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	level.Info(r.Logger).Log("controller", "NodeReconciler", "start reconcile", req.NamespacedName.String())
	defer level.Info(r.Logger).Log("controller", "NodeReconciler", "end reconcile", req.NamespacedName.String())

	var gateway v1beta1.Gateway
	err := r.Get(ctx, req.NamespacedName, &gateway)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	class := v1beta1.GatewayClass{}
	err = r.Get(ctx, client.ObjectKey{Name: string(gateway.Spec.GatewayClassName)}, &class)

	// Not our class
	if string(class.Spec.ControllerName) != r.ControllerName {
		return ctrl.Result{}, nil
	}

	var service corev1.Service
	name := serviceNameFromGateway(r.Namespace, gateway)
	err = r.Get(ctx, name, &service)
	if err != nil && errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if errors.IsNotFound(err) {
		// TODO create service on metallb's namespace
	}

	// service found, copy ip address. Set status

	return ctrl.Result{}, nil
}

func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Gateway{}).
		Complete(r)
}

func serviceNameFromGateway(namespace string, gateway v1beta1.Gateway) types.NamespacedName {
	return types.NamespacedName{
		Namespace: namespace,
		Name:      gateway.Name + "-svc",
	}
}

func serviceForGateway(gateway v1beta1.Gateway) corev1.Service {
	return corev1.Service{
		ObjectMeta: gateway.ObjectMeta,
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 80,
				},
			},
		},
	}
}
