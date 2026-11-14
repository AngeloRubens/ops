package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	api "github.com/nanovms/ops/lepton"
	"github.com/nanovms/ops/types"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func withGOOS(t *testing.T, goos string) {
	old := composeGOOS
	composeGOOS = goos
	t.Cleanup(func() { composeGOOS = old })
}

// the line in UP that calls validateComposeIPs is covered by the runtime runs of the branch only
func TestComposeParseIP(t *testing.T) {
	var y ComposeFile
	err := yaml.Unmarshal([]byte("packages:\n  - pkg: a\n    name: a:1\n    ip: 192.168.33.252\n  - pkg: b\n    name: b:1\n"), &y)
	assert.Nil(t, err)
	assert.Len(t, y.Packages, 2)
	assert.Equal(t, "192.168.33.252", y.Packages[0].IP)
	assert.Equal(t, "", y.Packages[1].IP)
}

func composeWithIPs(ips ...string) ComposeFile {
	var y ComposeFile
	for _, ip := range ips {
		y.Packages = append(y.Packages, ComposePackage{Pkg: "p" + ip, IP: ip})
	}
	return y
}

func TestValidateComposeIPs(t *testing.T) {
	withGOOS(t, "linux")

	tests := []struct {
		name string
		ips  []string
		want string // part of the error, "" for none
	}{
		{"no ip", []string{"", ""}, ""},
		{"valid", []string{"192.168.33.252", "192.168.33.254", ""}, ""},
		{"not an address", []string{"backend"}, "not a valid IPv4"},
		{"ipv6", []string{"fe80::1"}, "not a valid IPv4"},
		{"mapped ipv6", []string{"::ffff:192.168.33.252"}, "not a valid IPv4"},
		{"too short", []string{"192.168.33"}, "not a valid IPv4"},
		{"leading zero", []string{"192.168.33.0252"}, "not a valid IPv4"},
		{"other network", []string{"10.0.0.5"}, "outside the compose network"},
		{"next network", []string{"192.168.34.252"}, "outside the compose network"},
		{"bridge", []string{"192.168.33.1"}, "address of the bridge"},
		{"network address", []string{"192.168.33.0"}, "network address"},
		{"broadcast", []string{"192.168.33.255"}, "broadcast"},
		{"dhcp first", []string{"192.168.33.2"}, "DHCP range"},
		{"dhcp inside", []string{"192.168.33.100"}, "DHCP range"},
		{"dhcp last", []string{"192.168.33.251"}, "DHCP range"},
		{"duplicate", []string{"192.168.33.252", "", "192.168.33.252"}, "already used"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateComposeIPs(composeWithIPs(tt.ips...))
			if tt.want == "" {
				assert.Nil(t, err)
				return
			}
			if assert.NotNil(t, err) {
				assert.Contains(t, err.Error(), tt.want)
			}
		})
	}
}

func TestValidateComposeIPsNamesPackage(t *testing.T) {
	withGOOS(t, "linux")

	y := ComposeFile{Packages: []ComposePackage{{Pkg: "web"}, {Pkg: "db", IP: "10.1.1.1"}}}
	err := validateComposeIPs(y)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "package 2 (db)")
		assert.Contains(t, err.Error(), "10.1.1.1")
	}
}

func TestValidateComposeIPsOnlyOnLinux(t *testing.T) {
	withGOOS(t, "darwin")

	y := ComposeFile{Packages: []ComposePackage{{Pkg: "web"}, {Pkg: "db", IP: "192.168.33.252"}}}
	err := validateComposeIPs(y)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "package 2 (db)")
		assert.Contains(t, err.Error(), "supported only on linux")
	}

	// a file without ip is fine everywhere
	assert.Nil(t, validateComposeIPs(composeWithIPs("", "")))
}

// writeManifest writes a package.manifest with the given extra json and returns its path.
func writeManifest(t *testing.T, dir string, name string, runConfig string) string {
	p := filepath.Join(dir, name)
	body := `{"Program":"/bin/x"`
	if runConfig != "" {
		body += `,"RunConfig":` + runConfig
	}
	body += "}"
	assert.Nil(t, os.WriteFile(p, []byte(body), 0644))
	return p
}

type netFields struct{ ip, gw, mask, bridge string }

func fieldsOf(c *types.Config) netFields {
	rc := c.RunConfig
	return netFields{rc.IPAddress, rc.Gateway, rc.NetMask, rc.BridgeIPAddress}
}

// The config is shared by the packages of a compose: each step loads one package into the same config.
func TestLoadPackageConfig(t *testing.T) {
	none := netFields{}
	static := func(ip string) netFields { return netFields{ip, "192.168.33.1", "255.255.255.0", "192.168.33.1"} }

	type step struct {
		runConfig  string // RunConfig of the manifest
		comp       ComposePackage
		wantErr    string
		want       netFields // after the load
		wantVolume string
		after      netFields // after the restore
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{"static ip is set and put back", []step{
			{comp: ComposePackage{Pkg: "a", IP: "192.168.33.253"}, want: static("192.168.33.253"), after: none},
		}},
		{"address does not leak to the next package", []step{
			{comp: ComposePackage{Pkg: "a", IP: "192.168.33.253"}, want: static("192.168.33.253"), after: none},
			{comp: ComposePackage{Pkg: "b"}, want: none, after: none},
		}},
		{"two static packages", []step{
			{comp: ComposePackage{Pkg: "a", IP: "192.168.33.252"}, want: static("192.168.33.252"), after: none},
			{comp: ComposePackage{Pkg: "b", IP: "192.168.33.253"}, want: static("192.168.33.253"), after: none},
		}},
		{"static ip wins over the manifest, restore gives the manifest back", []step{
			{runConfig: `{"IPAddress":"10.1.1.1","Gateway":"10.1.1.254"}`, comp: ComposePackage{Pkg: "a", IP: "192.168.33.252"},
				want: static("192.168.33.252"), after: netFields{ip: "10.1.1.1", gw: "10.1.1.254"}},
		}},
		{"without ip nothing changes: the manifest is applied and stays, the restore does nothing", []step{
			{runConfig: `{"IPAddress":"10.9.9.9","Gateway":"10.9.9.1"}`, comp: ComposePackage{Pkg: "m", BaseVolumeSz: "2g"},
				want: netFields{ip: "10.9.9.9", gw: "10.9.9.1"}, wantVolume: "2g", after: netFields{ip: "10.9.9.9", gw: "10.9.9.1"}},
		}},
		{"ip with Nics is an error", []step{
			{runConfig: `{"Nics":[{"IPAddress":"10.1.1.1"}]}`, comp: ComposePackage{Pkg: "a", IP: "192.168.33.252"}, wantErr: "Nics"},
		}},
		{"Nics without ip are not looked at", []step{
			{runConfig: `{"Nics":[{"IPAddress":"10.1.1.1"}]}`, comp: ComposePackage{Pkg: "a"}},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			c := api.NewConfig()

			for i, st := range tt.steps {
				restore, err := loadPackageConfig(writeManifest(t, dir, strconv.Itoa(i), st.runConfig), st.comp, c)
				if st.wantErr != "" {
					if assert.NotNil(t, err) {
						assert.Contains(t, err.Error(), st.wantErr)
					}
					continue
				}
				assert.Nil(t, err)
				assert.Equal(t, st.want, fieldsOf(c), "step %d after load", i)
				if st.wantVolume != "" {
					assert.Equal(t, st.wantVolume, c.BaseVolumeSz)
				}
				restore()
				assert.Equal(t, st.after, fieldsOf(c), "step %d after restore", i)
			}
		})
	}
}

func withPing(t *testing.T, timeout time.Duration, ping func(string) (bool, string, error)) {
	oldPing, oldT, oldI := composePing, composePingTimeout, composePingInterval
	composePing = ping
	composePingTimeout, composePingInterval = timeout, time.Millisecond
	t.Cleanup(func() { composePing, composePingTimeout, composePingInterval = oldPing, oldT, oldI })
}

// the wiring of packageIP in UP is covered by the runtime runs of the branch, not by a unit test
func TestPackageIP(t *testing.T) {
	tests := []struct {
		name      string
		comp      ComposePackage
		timeout   time.Duration
		pingAfter int // the guest answers from this call on, 0 never
		pingErr   error
		wantIP    string
		wantErr   string
		wantArp   int
	}{
		{name: "static", comp: ComposePackage{Pkg: "backend", IP: "192.168.33.252"}, timeout: 10 * time.Second, pingAfter: 2, wantIP: "192.168.33.252"},
		{name: "static never answers", comp: ComposePackage{Pkg: "backend", IP: "192.168.33.252"}, timeout: 20 * time.Millisecond, wantErr: "backend: ip 192.168.33.252 does not answer ping after"},
		{name: "static ping fails", comp: ComposePackage{Pkg: "backend", IP: "192.168.33.252"}, timeout: time.Second, pingErr: errors.New("executable file not found"), wantErr: "backend: ip 192.168.33.252: executable file not found"},
		{name: "dhcp", comp: ComposePackage{Pkg: "backend"}, timeout: time.Second, wantIP: "192.168.33.77", wantArp: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guestPings := 0
			withPing(t, tt.timeout, func(host string) (bool, string, error) {
				guestPings++
				return tt.pingAfter != 0 && guestPings >= tt.pingAfter, "no reply", tt.pingErr
			})

			arp := 0
			old := findBridgedIP
			findBridgedIP = func(pid string) string { arp++; assert.Equal(t, "123", pid); return "192.168.33.77" }
			t.Cleanup(func() { findBridgedIP = old })

			com := Compose{config: &types.Config{}}
			ip, err := com.packageIP(tt.comp, "123")

			assert.Equal(t, tt.wantIP, ip)
			if tt.wantErr == "" {
				assert.Nil(t, err)
			} else if assert.NotNil(t, err) {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			// a package with ip never reads the arp table, one without never pings
			assert.Equal(t, tt.wantArp, arp)
			if tt.comp.IP == "" {
				assert.Equal(t, 0, guestPings)
			}
		})
	}
}

func TestCheckPingWorks(t *testing.T) {
	pings := 0
	answers, perr := true, error(nil)
	withPing(t, time.Second, func(host string) (bool, string, error) {
		pings++
		assert.Equal(t, composeBridgeIP, host)
		return answers, "output of ping", perr
	})

	// without ip ping is not used
	assert.Nil(t, checkPingWorks(composeWithIPs("", "")))
	assert.Equal(t, 0, pings)

	// the bridge is pinged once however many packages have an ip
	assert.Nil(t, checkPingWorks(composeWithIPs("", "192.168.33.252", "192.168.33.253")))
	assert.Equal(t, 1, pings)

	answers = false
	err := checkPingWorks(composeWithIPs("", "192.168.33.252"))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "p192.168.33.252: ip 192.168.33.252 needs a working ping")
		assert.Contains(t, err.Error(), "output of ping")
		assert.Contains(t, err.Error(), "remove ip to use DHCP")
	}

	perr = errors.New("executable file not found")
	err = checkPingWorks(composeWithIPs("192.168.33.252"))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "executable file not found")
	}
}

func TestPingWith(t *testing.T) {
	tests := []struct {
		name         string
		limit        time.Duration
		cmd          string
		args         []string
		wantAnswered bool
		wantErr      string
	}{
		{"reply", 5 * time.Second, "true", nil, true, ""},
		{"no reply", 5 * time.Second, "false", nil, false, ""},
		{"killed at the limit is no reply", 100 * time.Millisecond, "sleep", []string{"5"}, false, ""},
		{"cannot be run", 5 * time.Second, "no-such-ping-command", nil, false, "executable file not found"},
		{"other exit status keeps the output", 5 * time.Second, "sh", []string{"-c", "echo ping: socket: not permitted; exit 2"}, false, "not permitted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answered, _, err := pingWith(tt.limit, tt.cmd, tt.args...)
			assert.Equal(t, tt.wantAnswered, answered)
			if tt.wantErr == "" {
				assert.Nil(t, err)
			} else if assert.NotNil(t, err) {
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
