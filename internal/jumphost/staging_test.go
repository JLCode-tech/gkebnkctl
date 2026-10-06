package jumphost_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

type mintRecord struct{ count int }
type pushRecord struct{ keys []string }
type runRecord struct{ cmds []string }

func installSeams(
	t *testing.T,
	mint *mintRecord,
	pushes *pushRecord,
	runs *runRecord,
	runResponses map[string]string,
	errOn map[string]error,
) (restore func()) {
	t.Helper()

	origMint := *jumphost.PrepareIAPKeyFn
	origSSH := *jumphost.SSHRunViaIAPFn
	origPush := *jumphost.PushSSHPublicKeyFn

	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		mint.count++
		return "fake-key", "fake-pub", func() {}, nil
	}

	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, pub string) error {
		pushes.keys = append(pushes.keys, pub)
		return nil
	}

	*jumphost.SSHRunViaIAPFn = func(_ context.Context, _, _, _, cmd string) (string, error) {
		runs.cmds = append(runs.cmds, cmd)
		for key, err := range errOn {
			if strings.Contains(cmd, key) {
				return "", err
			}
		}
		for key, out := range runResponses {
			if strings.Contains(cmd, key) {
				return out, nil
			}
		}
		return "ok", nil
	}

	return func() {
		*jumphost.PrepareIAPKeyFn = origMint
		*jumphost.SSHRunViaIAPFn = origSSH
		*jumphost.PushSSHPublicKeyFn = origPush
	}
}

func testOpts() jumphost.ProbeOptions {
	return jumphost.ProbeOptions{
		Project:    "test-project",
		Zone:       "us-central1-a",
		Region:     "us-central1",
		InstanceID: "test-jumphost",
		SourceIP:   "10.0.10.120",
	}
}

func TestRunStagingCommands_MintOnce(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	restore := installSeams(t, mint, pushes, runs, nil, nil)
	defer restore()

	cmds := []string{"echo a", "echo b", "echo c"}
	out, err := jumphost.RunStagingCommands(context.Background(), testOpts(), cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mint.count != 1 {
		t.Errorf("prepareIAPKeyFn called %d times, want 1", mint.count)
	}
	if len(out) != 3 {
		t.Errorf("got %d stdout entries, want 3", len(out))
	}
}

func TestRunStagingCommands_RePushPerCommand(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	restore := installSeams(t, mint, pushes, runs, nil, nil)
	defer restore()

	cmds := []string{"echo a", "echo b", "echo c"}
	_, err := jumphost.RunStagingCommands(context.Background(), testOpts(), cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pushes.keys) != len(cmds) {
		t.Errorf("pushSSHPublicKeyFn called %d times, want %d (one per command)", len(pushes.keys), len(cmds))
	}
}

func TestRunStagingCommands_FailFast(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	bombErr := errors.New("command failed")
	restore := installSeams(t, mint, pushes, runs, nil, map[string]error{"FAIL": bombErr})
	defer restore()

	cmds := []string{"echo a", "FAIL me", "echo c"}
	out, err := jumphost.RunStagingCommands(context.Background(), testOpts(), cmds)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, bombErr) {
		t.Errorf("want bombErr, got %v", err)
	}
	if len(out) != 2 {
		t.Errorf("got %d stdout entries, want 2 (fail-fast after second cmd)", len(out))
	}
	for _, cmd := range runs.cmds {
		if strings.Contains(cmd, "echo c") {
			t.Error("third command was run after fail-fast — should have stopped")
		}
	}
}

func TestRunStagingCommands_CommandsRunInOrder(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	restore := installSeams(t, mint, pushes, runs, nil, nil)
	defer restore()

	cmds := []string{"first", "second", "third"}
	_, err := jumphost.RunStagingCommands(context.Background(), testOpts(), cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, want := range cmds {
		if i >= len(runs.cmds) || runs.cmds[i] != want {
			t.Errorf("cmd[%d]: got %q, want %q", i, runs.cmds[i], want)
		}
	}
}

func TestCopyFileViaIAP_Base64InRemoteCmd(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	restore := installSeams(t, mint, pushes, runs, nil, nil)
	defer restore()

	content := []byte("hello world\n")
	err := jumphost.CopyFileViaIAP(context.Background(), testOpts(), content, "/home/debian/test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs.cmds) != 1 {
		t.Fatalf("expected 1 ssh command, got %d", len(runs.cmds))
	}
	cmd := runs.cmds[0]
	if !strings.Contains(cmd, "base64 -d") {
		t.Errorf("remote cmd missing 'base64 -d': %q", cmd)
	}
	if !strings.Contains(cmd, "/home/debian/test.py") {
		t.Errorf("remote cmd missing remotePath: %q", cmd)
	}
	if !strings.Contains(cmd, "mkdir -p") {
		t.Errorf("remote cmd missing mkdir: %q", cmd)
	}
}

func TestCopyFileViaIAP_MintOnce(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	restore := installSeams(t, mint, pushes, runs, nil, nil)
	defer restore()

	err := jumphost.CopyFileViaIAP(context.Background(), testOpts(), []byte("data"), "/tmp/f.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mint.count != 1 {
		t.Errorf("prepareIAPKeyFn called %d times, want 1", mint.count)
	}
	if len(pushes.keys) != 1 {
		t.Errorf("pushSSHPublicKeyFn called %d times, want 1 (single-command copy)", len(pushes.keys))
	}
}

func TestCopyFileViaIAP_PropagatesError(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &runRecord{}
	copyErr := errors.New("ssh failed")
	restore := installSeams(t, mint, pushes, runs, nil, map[string]error{"base64": copyErr})
	defer restore()

	err := jumphost.CopyFileViaIAP(context.Background(), testOpts(), []byte("x"), "/tmp/x.py")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, copyErr) {
		t.Errorf("want copyErr, got %v", err)
	}
}

type stdinRunRecord struct {
	cmds   []string
	stdins []string
}

func installStdinSeams(t *testing.T, mint *mintRecord, pushes *pushRecord, runs *stdinRunRecord, runErr error) (restore func()) {
	t.Helper()

	origMint := *jumphost.PrepareIAPKeyFn
	origStdin := *jumphost.SSHRunViaIAPStdinFn
	origPush := *jumphost.PushSSHPublicKeyFn

	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		mint.count++
		return "fake-key", "fake-pub", func() {}, nil
	}
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, pub string) error {
		pushes.keys = append(pushes.keys, pub)
		return nil
	}
	*jumphost.SSHRunViaIAPStdinFn = func(_ context.Context, _, _, _, cmd string, stdin io.Reader) (string, error) {
		runs.cmds = append(runs.cmds, cmd)
		payload := ""
		if stdin != nil {
			b, _ := io.ReadAll(stdin)
			payload = string(b)
		}
		runs.stdins = append(runs.stdins, payload)
		if runErr != nil {
			return "", runErr
		}
		return "ok", nil
	}

	return func() {
		*jumphost.PrepareIAPKeyFn = origMint
		*jumphost.SSHRunViaIAPStdinFn = origStdin
		*jumphost.PushSSHPublicKeyFn = origPush
	}
}

func TestRunStagingCommandWithStdin_PassesStdinAndCommand(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &stdinRunRecord{}
	defer installStdinSeams(t, mint, pushes, runs, nil)()

	out, err := jumphost.RunStagingCommandWithStdin(
		context.Background(), testOpts(), "cat > /var/tmp/secret", strings.NewReader("s3cret\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ok" {
		t.Errorf("stdout: got %q, want %q", out, "ok")
	}
	if mint.count != 1 || len(pushes.keys) != 1 {
		t.Errorf("expected 1 mint + 1 push, got mint=%d push=%d", mint.count, len(pushes.keys))
	}
	if len(runs.cmds) != 1 || runs.cmds[0] != "cat > /var/tmp/secret" {
		t.Errorf("commands: got %v", runs.cmds)
	}
	if len(runs.stdins) != 1 || runs.stdins[0] != "s3cret\n" {
		t.Errorf("stdin payloads: got %q", runs.stdins)
	}
	if strings.Contains(runs.cmds[0], "s3cret") {
		t.Errorf("secret leaked into the command string: %q", runs.cmds[0])
	}
}

func TestCopyFileViaIAPStdin_ContentViaStdinNotArgv(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &stdinRunRecord{}
	defer installStdinSeams(t, mint, pushes, runs, nil)()

	content := []byte("-----BEGIN PRIVATE KEY-----\nfake-key-content-for-test\n-----END PRIVATE KEY-----\n")
	err := jumphost.CopyFileViaIAPStdin(context.Background(), testOpts(), content, "/home/debian/cert.pem")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs.cmds) != 1 {
		t.Fatalf("expected 1 ssh command, got %d", len(runs.cmds))
	}
	cmd := runs.cmds[0]
	if !strings.Contains(cmd, "mkdir -p") || !strings.Contains(cmd, "cat > '/home/debian/cert.pem'") {
		t.Errorf("remote cmd must mkdir + cat-to-path: %q", cmd)
	}
	if strings.Contains(cmd, "fake-key-content-for-test") || strings.Contains(cmd, "base64") {
		t.Errorf("file content leaked into the command string: %q", cmd)
	}
	if runs.stdins[0] != string(content) {
		t.Errorf("stdin payload: got %q, want the exact file content", runs.stdins[0])
	}
}

func TestCopyFileViaIAPStdin_PropagatesError(t *testing.T) {
	mint := &mintRecord{}
	pushes := &pushRecord{}
	runs := &stdinRunRecord{}
	copyErr := errors.New("ssh failed")
	defer installStdinSeams(t, mint, pushes, runs, copyErr)()

	err := jumphost.CopyFileViaIAPStdin(context.Background(), testOpts(), []byte("x"), "/tmp/x.pem")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, copyErr) {
		t.Errorf("want copyErr, got %v", err)
	}
}

func TestGrpcurlInstallCmd_SkipIfPresent(t *testing.T) {
	cmd := jumphost.GrpcurlInstallCmd()
	if !strings.Contains(cmd, "command -v grpcurl") {
		t.Errorf("cmd missing skip-if-present check: %q", cmd)
	}
}

func TestGrpcurlInstallCmd_PinnedVersion(t *testing.T) {
	cmd := jumphost.GrpcurlInstallCmd()
	if !strings.Contains(cmd, "v1.9.3") {
		t.Errorf("cmd does not pin v1.9.3: %q", cmd)
	}
	if !strings.Contains(cmd, "linux_x86_64") {
		t.Errorf("cmd does not target linux_x86_64: %q", cmd)
	}
}

func TestGrpcurlInstallCmd_InstallsToUsrLocalBin(t *testing.T) {
	cmd := jumphost.GrpcurlInstallCmd()
	if !strings.Contains(cmd, "/usr/local/bin") {
		t.Errorf("cmd does not install to /usr/local/bin: %q", cmd)
	}
}

func TestGrpcurlInstallCmd_PrintsVersion(t *testing.T) {
	cmd := jumphost.GrpcurlInstallCmd()
	if !strings.Contains(cmd, "grpcurl --version") {
		t.Errorf("cmd does not print version after install: %q", cmd)
	}
}
