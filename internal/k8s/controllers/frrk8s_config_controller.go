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
	"fmt"
	"reflect"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	frrv1beta1 "github.com/metallb/frrk8s/api/v1beta1"
	frrk8s "go.universe.tf/metallb/internal/bgp/frrk8s"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type frrk8sConfigEvent struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

func (evt *frrk8sConfigEvent) DeepCopyObject() runtime.Object {
	res := new(frrk8sConfigEvent)
	res.Name = evt.Name
	res.Namespace = evt.Namespace
	return res
}

func NewFRRK8sConfigEvent() event.GenericEvent {
	evt := frrk8sConfigEvent{}
	evt.Name = "reload"
	evt.Namespace = "frrk8sreload"
	return event.GenericEvent{Object: &evt}
}

type FRRK8sReconciler struct {
	client.Client
	Logger               log.Logger
	Scheme               *runtime.Scheme
	NodeName             string
	Namespace            string
	Reload               chan event.GenericEvent
	desiredConfiguration frrv1beta1.FRRConfiguration
	sync.Mutex
}

func (r *FRRK8sReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	fmt.Println("FEDE reconcile")
	level.Info(r.Logger).Log("controller", "FRRK8sReconciler", "start reconcile", req.NamespacedName.String())
	defer level.Info(r.Logger).Log("controller", "FRRK8sReconciler", "end reconcile", req.NamespacedName.String())
	updates.Inc()

	var current frrv1beta1.FRRConfiguration
	err := r.Get(ctx, req.NamespacedName, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	r.Lock()
	desired := r.desiredConfiguration.DeepCopy()
	r.Unlock()
	if reflect.DeepEqual(current.Spec, desired.Spec) {
		return ctrl.Result{}, nil
	}
	desired.Spec.DeepCopyInto(&current.Spec)
	current.Name = desired.Name
	current.Namespace = desired.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &current, func() error {
		desired.Spec.DeepCopyInto(&current.Spec)
		return nil
	})
	if err != nil {
		level.Info(r.Logger).Log("controller", "FRRConfiguration", "event", "failed to create frr8s configuration")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *FRRK8sReconciler) SetupWithManager(mgr ctrl.Manager) error {
	configName := frrk8s.ConfigName(r.NodeName)

	p := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		config, ok := obj.(*frrv1beta1.FRRConfiguration)
		if !ok {
			return true
		}
		if config.Name != configName {
			return false
		}
		if config.Namespace != r.Namespace {
			return false
		}
		return true
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&frrv1beta1.FRRConfiguration{}).
		WatchesRawSource(&source.Channel{Source: r.Reload}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(p).
		Complete(r)
}

func (r *FRRK8sReconciler) UpdateConfig(config interface{}) {
	fmt.Println("FEDE notified")
	r.Lock()
	defer r.Unlock()
	desired, ok := config.(frrv1beta1.FRRConfiguration)
	if !ok {
		panic("received an event that is not frr configuration")
	}
	r.desiredConfiguration = desired
	fmt.Println("FEDE sent on channel")
	r.Reload <- NewReloadEvent()
}
