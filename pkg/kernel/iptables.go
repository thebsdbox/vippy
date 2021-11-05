package kernel

import "os/exec"

//TODO - change the adapter so it works out side of Equinix Metal

// EnableMasq This handles the enabling of the MASQUERADING
func EnableMasq() error {
	if _, err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "bond0", "-j", "MASQUERADE").CombinedOutput(); err != nil {
		return err
	}

	return nil
}
