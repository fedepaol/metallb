package config

import (
	"fmt"

	"github.com/pkg/errors"
)

type Validate func(*configFile) error

// DiscardFRROnly returns an error if the current configFile contains
// any FRR only options
func DiscardFRROnly(c *configFile) error {
	for _, p := range c.Peers {
		if p.BFDProfile != "" {
			return fmt.Errorf("peer %s has bfd profile set on native bgp mode", p.Addr)
		}
		if p.KeepaliveTime != "" {
			return fmt.Errorf("peer %s has keepalive set on native bgp mode", p.Addr)
		}
	}
	if len(c.BFDProfiles) > 0 {
		return errors.New("bfd profiles section set")
	}
	for _, p := range c.Pools {
		for _, a := range p.BGPAdvertisements {
			if a.AggregationLengthV6 != nil {
				return fmt.Errorf("pool %s has aggregation lenghtv6 was set on native bgp mode", p.Name)
			}
		}
		if p.Protocol == BGP {
			for _, cidr := range p.Addresses {
				nets, err := ParseCIDR(cidr)
				if err != nil {
					return fmt.Errorf("invalid CIDR %q in pool %q: %s", cidr, p.Name, err)
				}
				for _, n := range nets {
					if n.IP.To4() == nil {
						return fmt.Errorf("pool %q has ipv6 CIDR %s", p.Name, n)
					}
				}
			}
		}
	}
	return nil
}

// DontValidate is a Validate function that always returns
// success.
func DontValidate(c *configFile) error {
	return nil
}
