package network

import (
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// AddIP - Add an IP address to the interface
func AddIP(ip string) error {
	link, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	address, err := netlink.ParseAddr(ip + "/32")
	address.Scope = unix.RT_SCOPE_HOST
	if err := netlink.AddrReplace(link, address); err != nil {
		return errors.Wrap(err, "could not add ip")
	}
	return nil
}

// DeleteIP - Remove an IP address from the interface
func DeleteIP(ip string) error {
	link, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	address, err := netlink.ParseAddr(ip + "/32")
	if err = netlink.AddrDel(link, address); err != nil {
		return errors.Wrap(err, "could not delete ip")
	}

	return nil
}
