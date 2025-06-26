// SPDX-License-Identifier:Apache-2.0

package allocator // import "go.universe.tf/metallb/internal/allocator"

import (
	"fmt"
	"net/netip"

	"go.universe.tf/metallb/internal/ipfamily"
	v1 "k8s.io/api/core/v1"
)

// Allocation represents a mapping of IP families (IPv4, IPv6, or both) to their respective IP addresses.
// This type is used to select and organize IPs based on a service's IP family policy.
// Depending on the family policy, the map may contain:
// - Just an IPv4 address
// - Just an IPv6 address
// - Both IPv4 and IPv6 addresses for dual-stack support.
//
// Key:
// - ipfamily.IPv4: Corresponds to the IPv4 address allocated for the service.
// - ipfamily.IPv6: Corresponds to the IPv6 address allocated for the service.
type Allocation struct {
	PoolName string
	IPV4     netip.Addr
	IPV6     netip.Addr
}

func (a *Allocation) getIPForFamily(family ipfamily.Family) netip.Addr {
	switch family {
	case ipfamily.IPv4:
		return a.IPV4
	case ipfamily.IPv6:
		return a.IPV6
	default:
		return netip.Addr{}
	}
}

func (a *Allocation) setIPForFamily(family ipfamily.Family, ip netip.Addr) {
	if !ip.IsValid() {
		return
	}
	switch family {
	case ipfamily.IPv4:
		a.IPV4 = ip
	case ipfamily.IPv6:
		a.IPV6 = ip
	}
}

// selectIPsForFamilyPolicy returns a slice of IPs from the ipPool, which are suitable for the
// given service ipfamily and IPFamilyPolicy.
func (a *Allocation) selectIPsForFamilyAndPolicy(
	serviceIPFamily ipfamily.Family,
	serviceIPFamilyPolicy v1.IPFamilyPolicy,
) ([]netip.Addr, error) {
	ipv4 := a.getIPForFamily(ipfamily.IPv4)
	ipv6 := a.getIPForFamily(ipfamily.IPv6)

	// if we don't have dual cluster ips, we should align the lb ip to the clusterip
	if serviceIPFamily == ipfamily.IPv4 {
		return []netip.Addr{ipv4}, nil
	}
	if serviceIPFamily == ipfamily.IPv6 {
		return []netip.Addr{ipv6}, nil
	}

	var ips []netip.Addr
	switch serviceIPFamilyPolicy {
	case v1.IPFamilyPolicySingleStack:
		if serviceIPFamily == ipfamily.IPv4 && ipv4.IsValid() {
			ips = append(ips, ipv4)
		} else if serviceIPFamily == ipfamily.IPv6 && ipv6.IsValid() {
			ips = append(ips, ipv6)
		}
	case v1.IPFamilyPolicyPreferDualStack, v1.IPFamilyPolicyRequireDualStack:
		if ipv4.IsValid() {
			ips = append(ips, ipv4)
		}
		if ipv6.IsValid() {
			ips = append(ips, ipv6)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no suitable IPs for family %s and policy %s", serviceIPFamily, serviceIPFamilyPolicy)
	}
	return ips, nil
}
