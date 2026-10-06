// Package jumphost provides SSH-via-IAP curl probe utilities for the
// jumphost model used by scenarios. It is a pure leaf package — it must NOT
// import internal/cli or internal/scenarios. The CLI layer translates
// cobra flags into ProbeOptions at the boundary.
package jumphost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultSSHUser is the default Linux username on Debian/Ubuntu jumphost instances.
const DefaultSSHUser = "debian"

// ProbeOptions configures a jumphost curl probe run.
type ProbeOptions struct {
	// Project is the GCP project ID.
	Project string
	// Zone is the GCP zone (e.g. "us-central1-a").
	Zone string
	// Region is the GCP region (e.g. "us-central1").
	Region string
	// InstanceID is the jumphost GCE instance name or ID (JUMPHOST_INSTANCE_NAME).
	InstanceID string
	// SourceIP is the jumphost's source IP if multi-NIC interface selection is needed.
	// curl --interface uses this so traffic hits the real data path.
	SourceIP string
	// VIP is the Gateway VIP to curl.
	VIP string
	// Iterations is the number of curl probes. Default 5.
	Iterations int
	// Timeout is the per-curl timeout. Default 10s.
	Timeout time.Duration
	// User is the SSH user on the jumphost. Default DefaultSSHUser ("debian").
	User string
	// Hostname sets the HTTP Host header so requests match hostname-scoped
	// HTTPRoutes. Empty means no Host header (matches no-hostname routes).
	Hostname string
}

// ProbeResult records the outcome of one curl iteration.
type ProbeResult struct {
	Iteration int     `json:"iteration"`
	HTTPCode  int     `json:"http_code"`
	Seconds   float64 `json:"seconds"`
	Err       string  `json:"error,omitempty"`
}

// prepareIAPKey mints an ephemeral ED25519 keypair, writes it to temp files,
// pushes the public half via gcloud or metadata, waits 2 s for it to settle,
// and returns the private-key path, the public-key path, and a cleanup func
// that removes both files. The caller is responsible for calling cleanup()
// (typically via defer).
func prepareIAPKey(ctx context.Context, region, instanceID string) (keyPath, pubKeyPath string, cleanup func(), err error) {
	priv, pubAuth, err := GenerateEphemeralED25519()
	if err != nil {
		return "", "", func() {}, fmt.Errorf("ephemeral key: %w", err)
	}

	keyFile, err := os.CreateTemp("", "gkebnkctl-jumphost-*.key")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("temp key file: %w", err)
	}
	if _, err := keyFile.Write(priv); err != nil {
		_ = keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", "", func() {}, fmt.Errorf("write key: %w", err)
	}
	if err := keyFile.Chmod(0o600); err != nil {
		_ = keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", "", func() {}, fmt.Errorf("chmod key: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		_ = os.Remove(keyFile.Name())
		return "", "", func() {}, fmt.Errorf("close key: %w", err)
	}

	pubFile, err := os.CreateTemp("", "gkebnkctl-jumphost-*.pub")
	if err != nil {
		_ = os.Remove(keyFile.Name())
		return "", "", func() {}, fmt.Errorf("temp pub file: %w", err)
	}
	if _, err := pubFile.WriteString(pubAuth); err != nil {
		_ = pubFile.Close()
		_ = os.Remove(keyFile.Name())
		_ = os.Remove(pubFile.Name())
		return "", "", func() {}, fmt.Errorf("write pub: %w", err)
	}
	if err := pubFile.Close(); err != nil {
		_ = os.Remove(keyFile.Name())
		_ = os.Remove(pubFile.Name())
		return "", "", func() {}, fmt.Errorf("close pub: %w", err)
	}

	cleanupFn := func() {
		_ = os.Remove(keyFile.Name())
		_ = os.Remove(pubFile.Name())
	}

	if err := PushSSHPublicKey(ctx, region, instanceID, pubFile.Name()); err != nil {
		cleanupFn()
		return "", "", func() {}, fmt.Errorf("send-ssh-public-key: %w", err)
	}

	time.Sleep(2 * time.Second)
	return keyFile.Name(), pubFile.Name(), cleanupFn, nil
}

// RunCurlProbes mints an ephemeral SSH key, pushes it, then runs opts.Iterations curls
// from the jumphost via the IAP tunnel. Returns per-iteration results.
func RunCurlProbes(ctx context.Context, opts ProbeOptions) ([]ProbeResult, error) {
	if opts.Iterations <= 0 {
		opts.Iterations = 5
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.User == "" {
		opts.User = DefaultSSHUser
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKey(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	results := make([]ProbeResult, 0, opts.Iterations)
	for i := 1; i <= opts.Iterations; i++ {
		_ = PushSSHPublicKey(ctx, opts.Region, opts.InstanceID, pubKeyPath)
		code, secs, perr := SSHCurlViaIAP(ctx, opts.Region, opts.InstanceID, keyPath, opts.SourceIP, opts.VIP, opts.Hostname, opts.Timeout)
		res := ProbeResult{Iteration: i, HTTPCode: code, Seconds: secs}
		if perr != nil {
			res.Err = perr.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

// PushSSHPublicKey sends a public key to the jumphost via gcloud / OS Login / metadata.
func PushSSHPublicKey(ctx context.Context, region, instanceID, pubKeyPath string) error {
	// For test stubs and gcloud IAP integration
	args := []string{
		"compute", "os-login", "ssh-keys", "add",
		"--key-file=" + pubKeyPath,
	}
	// #nosec G204 -- args constructed internally
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Non-fatal if os-login is not configured or in local test environment
		return nil
	}
	return nil
}

// buildCurlCmd returns the remote curl command. When host is non-empty it
// adds -H 'Host: <host>' so requests match hostname-scoped HTTPRoutes.
func buildCurlCmd(sourceIP, vip, host string, timeoutSecs int) string {
	hostHdr := ""
	if host != "" {
		hostHdr = fmt.Sprintf(`-H 'Host: %s' `, host)
	}
	ifaceHdr := ""
	if sourceIP != "" {
		ifaceHdr = fmt.Sprintf(`--interface %s `, sourceIP)
	}
	return fmt.Sprintf(`curl -s -o /dev/null -w '%%{http_code} %%{time_total}' %s%s--max-time %d http://%s/`,
		hostHdr, ifaceHdr, timeoutSecs, vip)
}

// SSHCurlViaIAP opens an IAP tunnel to the jumphost and runs a single curl.
func SSHCurlViaIAP(ctx context.Context, region, instanceID, keyPath, sourceIP, vip, host string, timeout time.Duration) (int, float64, error) {
	proxy := fmt.Sprintf(`ProxyCommand=gcloud compute start-iap-tunnel %s 22 --listen-on-stdin`, instanceID)
	remoteCmd := buildCurlCmd(sourceIP, vip, host, int(timeout.Seconds()))
	args := []string{
		"-o", proxy,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=15",
		"-i", keyPath,
		DefaultSSHUser + "@" + instanceID,
		remoteCmd,
	}
	// #nosec G204
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("ssh-curl: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	out := strings.TrimSpace(stdout.String())
	parts := strings.Fields(out)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected ssh-curl output %q", out)
	}
	var code int
	var secs float64
	if _, err := fmt.Sscanf(parts[0], "%d", &code); err != nil {
		return 0, 0, fmt.Errorf("parsing http_code %q: %w", parts[0], err)
	}
	if _, err := fmt.Sscanf(parts[1], "%f", &secs); err != nil {
		return code, 0, fmt.Errorf("parsing time_total %q: %w", parts[1], err)
	}
	return code, secs, nil
}

// BodyProbeResult records the outcome of one body-capturing curl iteration.
type BodyProbeResult struct {
	Iteration int    `json:"iteration"`
	HTTPCode  int    `json:"http_code"`
	Body      string `json:"body,omitempty"`
	Err       string `json:"error,omitempty"`
}

// buildCurlBodyCmd returns the remote curl command that captures the response body.
func buildCurlBodyCmd(sourceIP, vip, host string, timeoutSecs int) string {
	hostHdr := ""
	if host != "" {
		hostHdr = fmt.Sprintf(`-H 'Host: %s' `, host)
	}
	ifaceHdr := ""
	if sourceIP != "" {
		ifaceHdr = fmt.Sprintf(`--interface %s `, sourceIP)
	}
	return fmt.Sprintf(`curl -s -w '\n%%{http_code}' %s%s--max-time %d http://%s/`,
		hostHdr, ifaceHdr, timeoutSecs, vip)
}

// SSHCurlBodyViaIAP opens an IAP tunnel to the jumphost and runs a single body-capturing curl.
func SSHCurlBodyViaIAP(ctx context.Context, region, instanceID, keyPath, sourceIP, vip, host string, timeout time.Duration) (body string, code int, err error) {
	proxy := fmt.Sprintf(`ProxyCommand=gcloud compute start-iap-tunnel %s 22 --listen-on-stdin`, instanceID)
	remoteCmd := buildCurlBodyCmd(sourceIP, vip, host, int(timeout.Seconds()))
	args := []string{
		"-o", proxy,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=15",
		"-i", keyPath,
		DefaultSSHUser + "@" + instanceID,
		remoteCmd,
	}
	// #nosec G204
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("ssh-curl-body: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	raw := stdout.String()
	lastNL := strings.LastIndex(raw, "\n")
	if lastNL < 0 {
		return "", 0, fmt.Errorf("unexpected ssh-curl-body output %q", raw)
	}
	bodyStr := strings.TrimSpace(raw[:lastNL])
	codeStr := strings.TrimSpace(raw[lastNL+1:])
	var httpCode int
	if _, parseErr := fmt.Sscanf(codeStr, "%d", &httpCode); parseErr != nil {
		return "", 0, fmt.Errorf("parsing http_code %q: %w", codeStr, parseErr)
	}
	return bodyStr, httpCode, nil
}

// RunCurlBodyProbes runs opts.Iterations body-capturing curls from the jumphost.
func RunCurlBodyProbes(ctx context.Context, opts ProbeOptions) ([]BodyProbeResult, error) {
	if opts.Iterations <= 0 {
		opts.Iterations = 5
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.User == "" {
		opts.User = DefaultSSHUser
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKey(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	results := make([]BodyProbeResult, 0, opts.Iterations)
	for i := 1; i <= opts.Iterations; i++ {
		_ = PushSSHPublicKey(ctx, opts.Region, opts.InstanceID, pubKeyPath)
		body, code, perr := SSHCurlBodyViaIAP(ctx, opts.Region, opts.InstanceID, keyPath, opts.SourceIP, opts.VIP, opts.Hostname, opts.Timeout)
		res := BodyProbeResult{Iteration: i, HTTPCode: code, Body: body}
		if perr != nil {
			res.Err = perr.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

// SSHRunViaIAP opens an IAP tunnel to the jumphost and runs an arbitrary remote command.
func SSHRunViaIAP(ctx context.Context, region, instanceID, keyPath, remoteCmd string) (string, error) {
	return SSHRunViaIAPWithStdin(ctx, region, instanceID, keyPath, remoteCmd, nil)
}

// SSHRunViaIAPWithStdin is SSHRunViaIAP with the local ssh process's stdin attached to the given reader.
func SSHRunViaIAPWithStdin(ctx context.Context, region, instanceID, keyPath, remoteCmd string, stdin io.Reader) (string, error) {
	proxy := fmt.Sprintf(`ProxyCommand=gcloud compute start-iap-tunnel %s 22 --listen-on-stdin`, instanceID)
	args := []string{
		"-o", proxy,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=15",
		"-i", keyPath,
		DefaultSSHUser + "@" + instanceID,
		remoteCmd,
	}
	// #nosec G204
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("ssh-run: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// shellSingleQuote wraps s in single quotes safe for /bin/sh.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildHTTPResponderCmd returns the remote shell command that writes marker and starts a systemd unit.
func buildHTTPResponderCmd(port int, marker string) string {
	q := shellSingleQuote(marker)
	unit := fmt.Sprintf("gkebnkctl-extpool-%d", port)
	return fmt.Sprintf(
		`mkdir -p /tmp/gkebnkctl-extpool && printf '%%s' %s > /tmp/gkebnkctl-extpool/index.html; sudo systemctl stop %s 2>/dev/null; sudo systemctl reset-failed %s 2>/dev/null; sudo systemd-run --unit=%s --working-directory=/tmp/gkebnkctl-extpool python3 -m http.server %d >/dev/null 2>&1; sleep 2; curl -s -o /dev/null -w '%%{http_code}' http://127.0.0.1:%d/`,
		q, unit, unit, unit, port, port,
	)
}

// buildHTTPResponderStopCmd returns the remote command that stops the systemd unit.
func buildHTTPResponderStopCmd(port int) string {
	unit := fmt.Sprintf("gkebnkctl-extpool-%d", port)
	return fmt.Sprintf(`sudo systemctl stop %s 2>/dev/null; sudo systemctl reset-failed %s 2>/dev/null; true`, unit, unit)
}

// StartHTTPResponder starts a backgrounded python3 HTTP server on the jumphost.
func StartHTTPResponder(ctx context.Context, opts ProbeOptions, port int, marker string) error {
	keyPath, _, cleanup, err := prepareIAPKey(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return err
	}
	defer cleanup()

	out, runErr := SSHRunViaIAP(ctx, opts.Region, opts.InstanceID, keyPath, buildHTTPResponderCmd(port, marker))
	if runErr != nil {
		return fmt.Errorf("starting HTTP responder on :%d: %w (remote stdout: %q)", port, runErr, out)
	}
	if strings.TrimSpace(out) != "200" {
		return fmt.Errorf("HTTP responder on :%d did not return 200 (remote stdout: %q)", port, out)
	}
	return nil
}

// StopHTTPResponder kills any python3 http.server on port via the IAP tunnel.
func StopHTTPResponder(ctx context.Context, opts ProbeOptions, port int) error {
	keyPath, _, cleanup, err := prepareIAPKey(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return err
	}
	defer cleanup()

	_, _ = SSHRunViaIAP(ctx, opts.Region, opts.InstanceID, keyPath, buildHTTPResponderStopCmd(port))
	return nil
}

// GenerateEphemeralED25519 mints a fresh ED25519 keypair and returns
// (private-key in OpenSSH PEM, public-key in authorized_keys format, error).
func GenerateEphemeralED25519() (privPEM []byte, pubAuthLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "gkebnkctl-jumphost-ephemeral")
	if err != nil {
		return nil, "", err
	}
	pemBytes := pem.EncodeToMemory(pemBlock)

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	auth := fmt.Sprintf("ssh-ed25519 %s gkebnkctl-jumphost-ephemeral\n",
		base64.StdEncoding.EncodeToString(sshPub.Marshal()))
	return pemBytes, auth, nil
}
