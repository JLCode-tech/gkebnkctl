package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestCluster writes a minimal, valid cluster.yaml + dummy license.jwt
// under dir and returns the cluster.yaml path.
func writeTestCluster(t *testing.T, dir string) string {
	t.Helper()
	jwtPath := filepath.Join(dir, "license.jwt")
	if err := os.WriteFile(jwtPath, []byte("DUMMY-NOT-A-REAL-JWT\n"), 0o600); err != nil {
		t.Fatalf("writing dummy jwt: %v", err)
	}
	body := `
apiVersion: gkebnkctl/v1
kind: Cluster
metadata:
  name: bnk-hostdev
  project: my-gcp-project
  zone: us-central1-a
pattern: host-device
network:
  main:
    vpc: bnkhd-vpc
    subnet: bnkhd-subnet
    cidr: 10.60.0.0/24
    podsRangeName: bnkhd-pods
    podsCidr: 10.66.0.0/16
    svcsRangeName: bnkhd-svcs
    svcsCidr: 10.67.0.0/20
  external:
    vpc: bnkhd-ext
    subnet: bnkhd-ext
    cidr: 10.30.0.0/24
    mtu: 8896
    podsRangeName: tmm-ext-pods
    podsCidr: 10.31.0.0/24
    underlayCidr: 10.30.0.0/16
  internal:
    vpc: bnkhd-int
    subnet: bnkhd-int
    cidr: 10.50.0.0/24
    mtu: 8896
    podsRangeName: tmm-int-pods
    podsCidr: 10.51.0.0/24
  allowInternalCidr: 10.0.0.0/8
bnk:
  manifestVersion: "2.4.0-3.2882.0"
  jwtPath: ` + jwtPath + `
  farSecret:
    source: kubecontext
    sourceContext: some-other-cluster
`
	path := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing cluster.yaml: %v", err)
	}
	return path
}

// withCWD temporarily chdirs to dir for the duration of the test (state.Dir
// resolution in Cluster.StateDir() is relative to CWD), restoring the
// original working directory on cleanup.
func withCWD(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

// GKEBNKCTL_SKIP_AUTH=1 is the CI/test hook (see buildGCPClients) that lets
// --dry-run run end-to-end without live GCP credentials: Phase00 receives a
// nil *gcp.Clients and skips its ADC/API checks, and every other real phase
// never touches clients in its dry-run branch.

func TestRunPhasedUp_DryRun_NoStateFileWritten(t *testing.T) {
	dir := t.TempDir()
	path := writeTestCluster(t, dir)
	withCWD(t, dir)
	t.Setenv("GKEBNKCTL_SKIP_AUTH", "1")

	if err := runPhasedUp(context.Background(), path, true); err != nil {
		t.Fatalf("runPhasedUp(dryRun=true): unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gkebnkctl")); !os.IsNotExist(err) {
		t.Errorf("dry-run created .gkebnkctl state dir (stat err = %v); expected none", err)
	}
}

func TestRunPhasedUp_PrintsAllPhaseStubsAndRealPhaseBanners(t *testing.T) {
	dir := t.TempDir()
	path := writeTestCluster(t, dir)
	withCWD(t, dir)
	t.Setenv("GKEBNKCTL_SKIP_AUTH", "1")

	stderr := captureStderr(t, func() {
		if err := runPhasedUp(context.Background(), path, true); err != nil {
			t.Fatalf("runPhasedUp: unexpected error: %v", err)
		}
	})
	for _, p := range upPhases {
		if _, isReal := realUpPhases[p.num]; isReal {
			want := "[phase " + p.num + "]"
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing real-phase banner %q\nfull output:\n%s", want, stderr)
			}
			continue
		}
		want := "[phase " + p.num + "] " + p.name + " (stub)"
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing phase banner %q\nfull output:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stderr, "needs live cluster") {
		t.Error("stderr missing \"needs live cluster\" annotation for discovery-dependent stub phases")
	}
}

func TestRunPhasedUp_NonDryRun_SkipAuthWithoutDryRunErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeTestCluster(t, dir)
	withCWD(t, dir)
	t.Setenv("GKEBNKCTL_SKIP_AUTH", "1")

	err := runPhasedUp(context.Background(), path, false)
	if err == nil {
		t.Fatal("expected an error combining GKEBNKCTL_SKIP_AUTH=1 with a non-dry-run up, got nil")
	}
	if !strings.Contains(err.Error(), "only valid together with --dry-run") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".gkebnkctl")); !os.IsNotExist(statErr) {
		t.Errorf("rejected non-dry-run run created .gkebnkctl state dir (stat err = %v); expected none", statErr)
	}
}

func TestRunPhasedDown_DryRun_PrintsPlanAndNeverDeletesSharedVPCs(t *testing.T) {
	dir := t.TempDir()
	path := writeTestCluster(t, dir)
	withCWD(t, dir)

	stderr := captureStderr(t, func() {
		if err := runPhasedDown(context.Background(), path, false, true); err != nil {
			t.Fatalf("runPhasedDown(dryRun=true): unexpected error: %v", err)
		}
	})
	if !strings.Contains(stderr, "dry-run: destroy plan") {
		t.Errorf("stderr missing destroy-plan banner:\n%s", stderr)
	}
	if !strings.Contains(stderr, "NEVER deleted") {
		t.Errorf("stderr missing the shared-VPC-never-deleted line:\n%s", stderr)
	}
	if !strings.Contains(stderr, "bnkhd-ext") || !strings.Contains(stderr, "bnkhd-int") {
		t.Errorf("stderr should name the shared dataplane VPCs as never-deleted:\n%s", stderr)
	}
	// Main VPC + cluster ARE in the plan (they're what down deletes).
	if !strings.Contains(stderr, "bnkhd-vpc") {
		t.Errorf("stderr missing main VPC in destroy plan:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gkebnkctl")); !os.IsNotExist(err) {
		t.Errorf("down --dry-run created .gkebnkctl state dir (stat err = %v); expected none", err)
	}
}

func TestConfirmDestroy_AbortsOnNonAffirmativeInput(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("not-destroy\n")
	if confirmDestroy(in, &out, "bnk-hostdev", "proj", "zone") {
		t.Error("confirmDestroy returned true for non-affirmative input")
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("expected Aborted message, got: %s", out.String())
	}
}

func TestConfirmDestroy_AbortsOnEmptyStdin(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("")
	if confirmDestroy(in, &out, "bnk-hostdev", "proj", "zone") {
		t.Error("confirmDestroy returned true for empty/EOF stdin")
	}
}

func TestConfirmDestroy_ProceedsOnExactMatch(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("destroy\n")
	if !confirmDestroy(in, &out, "bnk-hostdev", "proj", "zone") {
		t.Error("confirmDestroy returned false for exact 'destroy' input")
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}
