// SPDX-License-Identifier:Apache-2.0

package service

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type Tweak func(svc *corev1.Service)

func TrafficPolicyLocal(svc *corev1.Service) {
	svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeLocal
}

func ForceV6(svc *corev1.Service) {
	f := corev1.IPFamilyPolicySingleStack
	svc.Spec.IPFamilyPolicy = &f
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
}

func DualStack(svc *corev1.Service) {
	f := corev1.IPFamilyPolicyRequireDualStack
	svc.Spec.IPFamilyPolicy = &f
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
}

func TrafficPolicyCluster(svc *corev1.Service) {
	svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeCluster
}

func tweakServicePort(svc *v1.Service) {
	if servicePodPort != 80 {
		// if servicePodPort is non default, then change service spec.
		svc.Spec.Ports[0].TargetPort = intstr.FromInt(int(servicePodPort))
	}
}

func tweakRCPort(rc *v1.ReplicationController) {
	if servicePodPort != 80 {
		// if servicePodPort is non default, then change pod's spec
		rc.Spec.Template.Spec.Containers[0].Args = []string{"netexec", fmt.Sprintf("--http-port=%d", servicePodPort), fmt.Sprintf("--udp-port=%d", servicePodPort)}
		rc.Spec.Template.Spec.Containers[0].ReadinessProbe.Handler.HTTPGet.Port = intstr.FromInt(int(servicePodPort))
	}
}
