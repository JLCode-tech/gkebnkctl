package cli

import (
	"testing"
	"time"
)

func TestTestDNSHelp(t *testing.T) {
	if testDNSCmd.Name() != "dns" {
		t.Errorf("expected command name 'dns', got %s", testDNSCmd.Name())
	}
}

func TestTestConnFlags(t *testing.T) {
	flagTestConnTarget = "127.0.0.1:9999"
	flagTestConnProto = "tcp"
	flagTestConnCount = 1
	flagTestConnTimeout = 100 * time.Millisecond
	flagTestConnJSON = false

	if err := runTestConnectivity(testConnCmd, nil); err != nil {
		t.Fatalf("runTestConnectivity failed: %v", err)
	}
}
