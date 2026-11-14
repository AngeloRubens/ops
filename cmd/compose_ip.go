package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nanovms/ops/types"
)

// The compose bridge is created by createBridgedNetwork(brName, "") in
// spawnDNS, which uses the default network of that function: the bridge
// has the address 192.168.33.1 in 192.168.33.0/24, and the dnsmasq that it
// starts serves DHCP on .2 to .251. These values must match
// createBridgedNetwork (cmd_network.go). Only .252 to .254 are left for
// the "ip" of a package.
const (
	composeBridgeIP   = "192.168.33.1"
	composeNetMask    = "255.255.255.0"
	composeSubnetBase = "192.168.33."

	// first and last host number of the dnsmasq range
	composeDHCPFirst = 2
	composeDHCPLast  = 251
)

// variables so that the tests can stand in for the machine
var (
	// how long a guest with a fixed address has to answer a ping. The
	// check is made against this deadline, a ping that is running when it
	// passes can add up to its own 2 second limit.
	composePingTimeout  = 20 * time.Second
	composePingInterval = 500 * time.Millisecond

	// composeGOOS is the system ops runs on.
	composeGOOS = runtime.GOOS

	// composePing sends one ping: answered tells if there was a reply; err is
	// set when ping could not be run at all (not installed, no permission).
	composePing = pingHost
)

// pingHost sends one ICMP echo to host.
func pingHost(host string) (answered bool, out string, err error) {
	return pingWith(2*time.Second, "ping", "-n", "-c", "1", "-W", "1", host)
}

// pingWith runs a ping command, giving up after limit. A ping that gets no reply (exit status 1) or is
// killed at the limit is not an error: the guest may just not be up yet. The output is returned for
// the messages; when the command cannot be run at all the error has the output too.
func pingWith(limit time.Duration, name string, args ...string) (answered bool, out string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	out = strings.TrimSpace(string(b))
	if err == nil {
		return true, out, nil
	}

	if ctx.Err() != nil {
		return false, out, nil
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, out, nil
	}

	if out != "" {
		err = fmt.Errorf("%v: %s", err, out)
	}
	return false, out, err
}

func hasStaticIP(y ComposeFile) bool {
	for _, comp := range y.Packages {
		if comp.IP != "" {
			return true
		}
	}
	return false
}

// validateComposeIPs checks the "ip" of the packages of a compose file.
func validateComposeIPs(y ComposeFile) error {
	if !hasStaticIP(y) {
		return nil
	}

	if composeGOOS != "linux" {
		for i, comp := range y.Packages {
			if comp.IP != "" {
				return fmt.Errorf("package %d (%s): ip is supported only on linux, not on %s", i+1, comp.Pkg, composeGOOS)
			}
		}
	}

	seen := map[string]string{}

	for i, comp := range y.Packages {
		if comp.IP == "" {
			continue
		}

		who := fmt.Sprintf("package %d (%s)", i+1, comp.Pkg)

		// the ":" test refuses the forms of an IPv4 address inside IPv6 ("::ffff:a.b.c.d"), which
		// ParseIP accepts and To4 converts
		ip := net.ParseIP(comp.IP)
		if ip == nil || ip.To4() == nil || strings.Contains(comp.IP, ":") {
			return fmt.Errorf("%s: ip %q is not a valid IPv4 address", who, comp.IP)
		}

		v4 := ip.To4()
		if !strings.HasPrefix(comp.IP, composeSubnetBase) {
			return fmt.Errorf("%s: ip %s is outside the compose network %s0/24", who, comp.IP, composeSubnetBase)
		}

		switch last := int(v4[3]); {
		case comp.IP == composeBridgeIP:
			return fmt.Errorf("%s: ip %s is the address of the bridge %s", who, comp.IP, genBridgeName())
		case last == 0:
			return fmt.Errorf("%s: ip %s is the network address of %s0/24", who, comp.IP, composeSubnetBase)
		case last == 255:
			return fmt.Errorf("%s: ip %s is the broadcast address of %s0/24", who, comp.IP, composeSubnetBase)
		case last >= composeDHCPFirst && last <= composeDHCPLast:
			return fmt.Errorf("%s: ip %s is inside the DHCP range %s%d-%s%d of the compose network; use %s%d-%s254",
				who, comp.IP, composeSubnetBase, composeDHCPFirst, composeSubnetBase, composeDHCPLast,
				composeSubnetBase, composeDHCPLast+1, composeSubnetBase)
		}

		if other, ok := seen[comp.IP]; ok {
			return fmt.Errorf("%s: ip %s is already used by %s", who, comp.IP, other)
		}
		seen[comp.IP] = who
	}

	return nil
}

// checkPingWorks makes sure, before any guest is started, that the host can ping the bridge. The
// guests with an ip are waited for with ping, and without this a broken ping would be blamed on them.
func checkPingWorks(y ComposeFile) error {
	for _, comp := range y.Packages {
		if comp.IP == "" {
			continue
		}

		answered, out, err := composePing(composeBridgeIP)
		if err != nil {
			out = err.Error()
		} else if answered {
			return nil
		}

		return fmt.Errorf("%s: ip %s needs a working ping, and ping of the bridge %s does not work here (%s); remove ip to use DHCP", comp.Pkg, comp.IP, composeBridgeIP, out)
	}

	return nil
}

// loadPackageConfig reads the config of a package into c, as compose
// always did (an error of the config is not reported, as before), and
// applies the base volume size and the ip of the compose entry.
//
// The config is shared by all the packages of the compose. A package
// without ip changes nothing here beyond what it always did. For a package
// with ip the returned function puts back the network fields as they were
// after the config was read, so that the address does not stay for the
// next package; for one without it does nothing.
func loadPackageConfig(ppath string, comp ComposePackage, c *types.Config) (func(), error) {
	unWarpConfig(ppath, c)

	if comp.BaseVolumeSz != "" {
		c.BaseVolumeSz = comp.BaseVolumeSz
	}

	if comp.IP == "" {
		return func() {}, nil
	}

	rc := &c.RunConfig

	// in a package path the manifest does not take Nics, but qemu adds a card for each of them. The
	// config is shared, so they can come from an earlier package too (nanovms/ops issue 1814).
	// Nics after a field that cannot be decoded are not seen: unWarpConfig stops there, as always.
	if len(rc.Nics) > 0 {
		return func() {}, fmt.Errorf("%s: ip cannot be used with Nics in the config (of this package or an earlier one, see nanovms/ops issue 1814)", comp.Pkg)
	}

	ip, gw, mask, bridgeIP := rc.IPAddress, rc.Gateway, rc.NetMask, rc.BridgeIPAddress

	rc.IPAddress = comp.IP
	rc.Gateway = composeBridgeIP
	rc.NetMask = composeNetMask
	// without it onprem moves the bridge to .66 of the guest network
	rc.BridgeIPAddress = composeBridgeIP

	return func() {
		rc.IPAddress, rc.Gateway, rc.NetMask, rc.BridgeIPAddress = ip, gw, mask, bridgeIP
	}, nil
}

// packageIP waits until the guest of comp is up and returns its address:
// the one of the compose file when there is one (a ping must be answered,
// there is nothing to look up in the arp table of the host), else the one
// that the guest got by DHCP.
func (com Compose) packageIP(comp ComposePackage, pid string) (string, error) {
	if comp.IP == "" {
		return com.waitForIP(pid)
	}

	last := ""
	for start := time.Now(); ; {
		answered, out, err := composePing(comp.IP)
		if err != nil {
			return "", fmt.Errorf("%s: ip %s: %v", comp.Pkg, comp.IP, err)
		}
		if answered {
			return comp.IP, nil
		}
		last = out

		if time.Since(start) >= composePingTimeout {
			break
		}
		time.Sleep(composePingInterval)
	}

	return "", fmt.Errorf("%s: ip %s does not answer ping after %s: %s", comp.Pkg, comp.IP, composePingTimeout, last)
}
