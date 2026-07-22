package phases

import (
	"os/exec"
	"strings"
	"testing"
)

// The ifaceDumpScript must resolve a netdev's PCI BDF, not the virtio child
// node. Regression guard: the fix markers (BDF-shape case + parent fallback)
// must stay in the shipped script.
func TestIfaceDumpScript_KeepsVirtioPCIFix(t *testing.T) {
	if !strings.Contains(ifaceDumpScript, "*:*:*.*)") {
		t.Error("ifaceDumpScript lost the PCI-BDF-shape case — virtio NICs will report the wrong PCI device")
	}
	if !strings.Contains(ifaceDumpScript, `basename "$(dirname "$dev")"`) {
		t.Error("ifaceDumpScript lost the parent-dir fallback — virtio NICs will report e.g. 'virtio2' instead of '0000:00:05.0'")
	}
}

// Behavioural check of the PCI-resolution logic against the device-symlink
// shapes GCE produces: virtio-net nests the PCI function under a virtioN node;
// gVNIC and direct-PCI expose the BDF as the leaf. Proven live 2026-07-22:
// with the wrong value ("virtio3") mapres never maps net1/net2 and TMM crashes
// with tcl_init_proxy failed; with "0000:00:06.0" TMM reaches F5TmmAvailable=True.
func TestPCIResolution_HandlesVirtioAndDirectPCI(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// Same case logic as ifaceDumpScript, isolated to a single path arg.
	const resolve = `dev="$1"; leaf=$(basename "$dev"); case "$leaf" in *:*:*.*) echo "$leaf";; *) basename "$(dirname "$dev")";; esac`
	cases := map[string]string{
		"/sys/devices/pci0000:00/0000:00:05.0/virtio2": "0000:00:05.0", // virtio-net (GCE default)
		"/sys/devices/pci0000:00/0000:00:06.0":         "0000:00:06.0", // direct PCI (bnk-hostdev)
		"/sys/devices/pci0000:03/0000:03:00.0/gve":     "0000:03:00.0", // gVNIC
	}
	for path, want := range cases {
		out, err := exec.Command("sh", "-c", resolve, "sh", path).Output()
		if err != nil {
			t.Fatalf("running resolve for %s: %v", path, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("PCI resolve(%s) = %q, want %q", path, got, want)
		}
	}
}
