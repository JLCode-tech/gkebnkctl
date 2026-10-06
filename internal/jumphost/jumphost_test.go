package jumphost_test

import (
	"crypto/ed25519"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

func TestGenerateEphemeralED25519_ParseablePEM(t *testing.T) {
	privPEM, pubAuth, err := jumphost.GenerateEphemeralED25519()
	if err != nil {
		t.Fatalf("GenerateEphemeralED25519: %v", err)
	}

	block, _ := pem.Decode(privPEM)
	if block == nil {
		t.Fatal("privPEM is not a valid PEM block")
	}
	if !strings.Contains(block.Type, "OPENSSH PRIVATE KEY") && !strings.Contains(block.Type, "PRIVATE KEY") {
		t.Errorf("unexpected PEM type %q (want OPENSSH PRIVATE KEY)", block.Type)
	}

	privKey, err := ssh.ParseRawPrivateKey(privPEM)
	if err != nil {
		t.Fatalf("ssh.ParseRawPrivateKey: %v", err)
	}
	if _, ok := privKey.(*ed25519.PrivateKey); !ok {
		t.Logf("key type: %T (ok — may be a wrapped type)", privKey)
	}

	if !strings.HasPrefix(pubAuth, "ssh-ed25519 ") {
		t.Errorf("pubAuth does not start with 'ssh-ed25519 ': %q", pubAuth[:min(len(pubAuth), 40)])
	}

	if !strings.HasSuffix(pubAuth, "\n") {
		t.Errorf("pubAuth does not end with newline")
	}
}

func TestGenerateEphemeralED25519_Unique(t *testing.T) {
	p1, _, err1 := jumphost.GenerateEphemeralED25519()
	p2, _, err2 := jumphost.GenerateEphemeralED25519()
	if err1 != nil || err2 != nil {
		t.Fatalf("GenerateEphemeralED25519 error: %v / %v", err1, err2)
	}
	if string(p1) == string(p2) {
		t.Error("two generated keys are identical — random source broken")
	}
}

func TestBuildCurlCmd_WithHost(t *testing.T) {
	cmd := jumphost.BuildCurlCmd("10.0.10.120", "10.0.10.100", "gkebnkctl.local", 10)

	if !strings.Contains(cmd, `-H 'Host: gkebnkctl.local'`) {
		t.Errorf("cmd missing Host header: %q", cmd)
	}
	if !strings.Contains(cmd, "http://10.0.10.100/") {
		t.Errorf("cmd missing VIP URL: %q", cmd)
	}
	if !strings.Contains(cmd, "--interface 10.0.10.120") {
		t.Errorf("cmd missing --interface sourceIP: %q", cmd)
	}
}

func TestBuildCurlCmd_NoHost(t *testing.T) {
	cmd := jumphost.BuildCurlCmd("10.0.10.120", "10.0.10.100", "", 10)

	if strings.Contains(cmd, `-H 'Host:`) {
		t.Errorf("cmd should not contain Host header when host is empty: %q", cmd)
	}
	if !strings.Contains(cmd, "http://10.0.10.100/") {
		t.Errorf("cmd missing VIP URL: %q", cmd)
	}
	if !strings.Contains(cmd, "--interface 10.0.10.120") {
		t.Errorf("cmd missing --interface sourceIP: %q", cmd)
	}
}

func TestBuildCurlBodyCmd_WithHost(t *testing.T) {
	cmd := jumphost.BuildCurlBodyCmd("10.0.10.120", "10.0.10.100", "split.local", 10)

	if !strings.Contains(cmd, `-H 'Host: split.local'`) {
		t.Errorf("cmd missing Host header: %q", cmd)
	}
	if strings.Contains(cmd, "-o /dev/null") {
		t.Errorf("cmd must NOT contain -o /dev/null for body capture: %q", cmd)
	}
	if !strings.Contains(cmd, `-w '\n%{http_code}'`) {
		t.Errorf("cmd missing body+code format string: %q", cmd)
	}
	if !strings.Contains(cmd, "http://10.0.10.100/") {
		t.Errorf("cmd missing VIP URL: %q", cmd)
	}
	if !strings.Contains(cmd, "--interface 10.0.10.120") {
		t.Errorf("cmd missing --interface sourceIP: %q", cmd)
	}
}

func TestBuildCurlBodyCmd_NoHost(t *testing.T) {
	cmd := jumphost.BuildCurlBodyCmd("10.0.10.120", "10.0.10.100", "", 10)

	if strings.Contains(cmd, `-H 'Host:`) {
		t.Errorf("cmd should not contain Host header when host is empty: %q", cmd)
	}
	if strings.Contains(cmd, "-o /dev/null") {
		t.Errorf("cmd must NOT contain -o /dev/null for body capture: %q", cmd)
	}
	if !strings.Contains(cmd, `-w '\n%{http_code}'`) {
		t.Errorf("cmd missing body+code format string: %q", cmd)
	}
	if !strings.Contains(cmd, "http://10.0.10.100/") {
		t.Errorf("cmd missing VIP URL: %q", cmd)
	}
}

func TestBuildHTTPResponderCmd_ContainsMarkerAndPort(t *testing.T) {
	cmd := jumphost.BuildHTTPResponderCmd(8080, "external-resource-pool-OK")

	if !strings.Contains(cmd, "external-resource-pool-OK") {
		t.Errorf("cmd missing marker: %q", cmd)
	}
	if !strings.Contains(cmd, "systemd-run") {
		t.Errorf("cmd missing 'systemd-run': %q", cmd)
	}
	if !strings.Contains(cmd, "gkebnkctl-extpool-8080") {
		t.Errorf("cmd missing unit name 'gkebnkctl-extpool-8080': %q", cmd)
	}
	if !strings.Contains(cmd, "http.server 8080") {
		t.Errorf("cmd missing 'http.server 8080': %q", cmd)
	}
	if !strings.Contains(cmd, "http://127.0.0.1:8080/") {
		t.Errorf("cmd missing self-curl URL: %q", cmd)
	}
	if !strings.Contains(cmd, "%{http_code}") {
		t.Errorf("cmd missing http_code probe: %q", cmd)
	}
}

func TestBuildHTTPResponderCmd_EscapesMarkerQuotes(t *testing.T) {
	cmd := jumphost.BuildHTTPResponderCmd(9090, "a'b")
	if !strings.Contains(cmd, `'a'\''b'`) {
		t.Errorf("single-quote in marker not escaped: %q", cmd)
	}
}

func TestBuildHTTPResponderStopCmd(t *testing.T) {
	cmd := jumphost.BuildHTTPResponderStopCmd(8080)
	if !strings.Contains(cmd, "systemctl stop") {
		t.Errorf("stop cmd missing 'systemctl stop': %q", cmd)
	}
	if !strings.Contains(cmd, "gkebnkctl-extpool-8080") {
		t.Errorf("stop cmd missing unit name 'gkebnkctl-extpool-8080': %q", cmd)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
