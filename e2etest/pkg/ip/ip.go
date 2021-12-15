package ip

import (
	"fmt"

	"go.universe.tf/metallb/internal/config"

	"github.com/mikioh/ipaddr"
	"github.com/pkg/errors"
)

func GetFromRangeByIndex(ipRange string, index int) (string, error) {
	cidrs, err := config.ParseCIDR(ipRange)
	if err != nil {
		return "", errors.Wrapf(err, "Failed to parse CIDR while getting IP from range by index")
	}

	i := 0
	var c *ipaddr.Cursor
	for _, cidr := range cidrs {
		c = ipaddr.NewCursor([]ipaddr.Prefix{*ipaddr.NewPrefix(cidr)})
		for i < index && c.Next() != nil {
			i++
		}
		if i == index {
			return c.Pos().IP.String(), nil
		}
		i++
	}

	return "", fmt.Errorf("failed to get IP in index %d from range %s", index, ipRange)
}
