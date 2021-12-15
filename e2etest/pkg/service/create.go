package service

import (
	corev1 "k8s.io/api/core/v1"
	service "k8s.io/kubernetes/test/e2e/framework/service"

	"github.com/docker/cli/kubernetes/client/clientset"
	"k8s.io/kubernetes/test/e2e/framework"
)

func CreateWithBackend(cs clientset.Interface, namespace string, jigName string, tweak ...func(svc *corev1.Service)) (*corev1.Service, *e2eservice.TestJig) {
	var svc *corev1.Service
	var err error

	jig := service.NewTestJig(cs, namespace, jigName)
	timeout := service.GetServiceLoadBalancerCreationTimeout(cs)
	svc, err = jig.CreateLoadBalancerService(timeout, func(svc *corev1.Service) {
		tweakServicePort(svc)
		for _, f := range tweak {
			f(svc)
		}
	})

	framework.ExpectNoError(err)
	_, err = jig.Run(func(rc *corev1.ReplicationController) {
		tweakRCPort(rc)
	})
	framework.ExpectNoError(err)
	return svc, jig
}
