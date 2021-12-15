// SPDX-License-Identifier:Apache-2.0

package e2e

import (
	"fmt"
	"net"
	"os/exec"

	"go.universe.tf/metallb/e2etest/pkg/config"
	"go.universe.tf/metallb/e2etest/pkg/executor"
	internalconfig "go.universe.tf/metallb/internal/config"
	"k8s.io/kubernetes/test/e2e/framework"
)

const (
	NetworkFailure = 4
	retryLimit     = 4
)

// DescribeSvc logs the output of kubectl describe svc for the given namespace.
func DescribeSvc(ns string) {
	framework.Logf("\nOutput of kubectl describe svc:\n")
	desc, _ := framework.RunKubectl(
		ns, "describe", "svc", fmt.Sprintf("--namespace=%v", ns))
	framework.Logf(desc)
}

func wgetRetry(address string, exc executor.Executor) error {
	retrycnt := 0
	code := 0
	var err error

	// Retry loop to handle wget NetworkFailure errors
	for {
		_, err = exc.Exec("wget", "-O-", "-q", address, "-T", "60")
		if exitErr, ok := err.(*exec.ExitError); err != nil && ok {
			code = exitErr.ExitCode()
		} else {
			break
		}
		if retrycnt < retryLimit && code == NetworkFailure {
			framework.Logf(" wget failed with code %d, err %s retrycnt %d\n", code, err, retrycnt)
			retrycnt++
		} else {
			break
		}
	}
	return err
}

func validateIPInRange(addressPools []config.AddressPool, ip string) error {
	input := net.ParseIP(ip)
	for _, addressPool := range addressPools {
		for _, address := range addressPool.Addresses {
			cidrs, err := internalconfig.ParseCIDR(address)
			framework.ExpectNoError(err)
			for _, cidr := range cidrs {
				if cidr.Contains(input) {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("ip %s is not in AddressPool range", ip)
}
