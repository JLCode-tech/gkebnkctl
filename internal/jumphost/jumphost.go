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

// DefaultProbePod is the default in-cluster netshoot probe pod name.
const DefaultProbePod = "test-probe"

// DefaultProbeNamespace is the default namespace for the in-cluster probe pod.
const DefaultProbeNamespace = "default"

func probePodName() string {
	if pod := os.Getenv("GKEBNKCTL_PROBE_POD"); pod != "" {
		return pod
	}
	return DefaultProbePod
}

func probePodNamespace() string {
	if ns := os.Getenv("GKEBNKCTL_PROBE_NAMESPACE"); ns != "" {
		return ns
	}
	return DefaultProbeNamespace
}

// IsProbePodRunning checks if the in-cluster probe pod is available and running.
func IsProbePodRunning(ctx context.Context) bool {
	pod := probePodName()
	ns := probePodNamespace()
	cmd := exec.CommandContext(ctx, "kubectl", "get", "pod", pod, "-n", ns, "-o", "jsonpath={.status.phase}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false
	}
	return strings.TrimSpace(out.String()) == "Running"
}

// ProbePodIP returns the IP address of the in-cluster probe pod if running, or "" if unavailable.
func ProbePodIP(ctx context.Context) string {
	pod := probePodName()
	ns := probePodNamespace()
	cmd := exec.CommandContext(ctx, "kubectl", "get", "pod", pod, "-n", ns, "-o", "jsonpath={.status.podIP}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func execInProbe(ctx context.Context, cmdStr string) (string, error) {
	pod := probePodName()
	ns := probePodNamespace()
	args := []string{"exec", "-n", ns, pod, "--", "sh", "-c", cmdStr}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("probe-exec: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func execInProbeWithStdin(ctx context.Context, cmdStr string, stdin io.Reader) (string, error) {
	pod := probePodName()
	ns := probePodNamespace()
	args := []string{"exec", "-i", "-n", ns, pod, "--", "sh", "-c", cmdStr}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("probe-exec: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

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

// RunCurlProbes runs opts.Iterations curls. If an in-cluster probe pod is available
// (or GKEBNKCTL_PROBE_IN_CLUSTER is set), curls execute via kubectl exec on the probe pod;
// otherwise it opens an IAP tunnel to the jumphost.
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

	useProbe := os.Getenv("GKEBNKCTL_FORCE_IAP") != "1" && (os.Getenv("GKEBNKCTL_PROBE_IN_CLUSTER") != "" || IsProbePodRunning(ctx))
	if useProbe {
		results := make([]ProbeResult, 0, opts.Iterations)
		remoteCmd := BuildCurlCmd("", opts.VIP, opts.Hostname, int(opts.Timeout.Seconds()))
		for i := 1; i <= opts.Iterations; i++ {
			out, perr := execInProbe(ctx, remoteCmd)
			res := ProbeResult{Iteration: i}
			if perr != nil {
				res.Err = perr.Error()
			}
			parts := strings.Fields(out)
			if len(parts) >= 2 {
				var code int
				var secs float64
				_, _ = fmt.Sscanf(parts[0], "%d", &code)
				_, _ = fmt.Sscanf(parts[1], "%f", &secs)
				res.HTTPCode = code
				res.Seconds = secs
			} else if len(parts) == 1 {
				var code int
				_, _ = fmt.Sscanf(parts[0], "%d", &code)
				res.HTTPCode = code
			}
			results = append(results, res)
		}
		return results, nil
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

// BuildCurlCmd returns the remote curl command. When host is non-empty it
// adds -H 'Host: <host>' so requests match hostname-scoped HTTPRoutes.
func BuildCurlCmd(sourceIP, vip, host string, timeoutSecs int) string {
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
	remoteCmd := BuildCurlCmd(sourceIP, vip, host, int(timeout.Seconds()))
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

// RunCurlBodyProbes runs opts.Iterations body-capturing curls from the jumphost or probe pod.
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

	useProbe := os.Getenv("GKEBNKCTL_FORCE_IAP") != "1" && (os.Getenv("GKEBNKCTL_PROBE_IN_CLUSTER") != "" || IsProbePodRunning(ctx))
	if useProbe {
		results := make([]BodyProbeResult, 0, opts.Iterations)
		remoteCmd := buildCurlBodyCmd("", opts.VIP, opts.Hostname, int(opts.Timeout.Seconds()))
		for i := 1; i <= opts.Iterations; i++ {
			raw, perr := execInProbe(ctx, remoteCmd)
			res := BodyProbeResult{Iteration: i}
			if perr != nil {
				res.Err = perr.Error()
			}
			lastNL := strings.LastIndex(raw, "\n")
			if lastNL >= 0 {
				res.Body = strings.TrimSpace(raw[:lastNL])
				codeStr := strings.TrimSpace(raw[lastNL+1:])
				var httpCode int
				if _, parseErr := fmt.Sscanf(codeStr, "%d", &httpCode); parseErr == nil {
					res.HTTPCode = httpCode
				}
			} else if raw != "" {
				var httpCode int
				if _, parseErr := fmt.Sscanf(strings.TrimSpace(raw), "%d", &httpCode); parseErr == nil {
					res.HTTPCode = httpCode
				} else {
					res.Body = strings.TrimSpace(raw)
				}
			}
			results = append(results, res)
		}
		return results, nil
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

// buildProbeHTTPResponderCmd returns the command that writes marker and runs python3 http.server inside a probe container.
func buildProbeHTTPResponderCmd(port int, marker string) string {
	q := shellSingleQuote(marker)
	dir := fmt.Sprintf("/tmp/gkebnkctl-extpool-%d", port)
	return fmt.Sprintf(
		`mkdir -p %s && printf '%%s' %s > %s/index.html && pkill -f '\[p\]ython3 -m http.server %d' 2>/dev/null || true; nohup python3 -m http.server %d --directory %s </dev/null >/dev/null 2>&1 & sleep 1; curl -s -o /dev/null -w '%%{http_code}' http://127.0.0.1:%d/`,
		dir, q, dir, port, port, dir, port,
	)
}

// buildProbeHTTPResponderStopCmd returns the command that stops python3 http.server inside a probe container.
func buildProbeHTTPResponderStopCmd(port int) string {
	return fmt.Sprintf(`pkill -f '\[p\]ython3 -m http.server %d' 2>/dev/null || true`, port)
}

// StartHTTPResponder starts a backgrounded python3 HTTP server on the jumphost or in-cluster probe pod.
func StartHTTPResponder(ctx context.Context, opts ProbeOptions, port int, marker string) error {
	useProbe := os.Getenv("GKEBNKCTL_FORCE_IAP") != "1" && (os.Getenv("GKEBNKCTL_PROBE_IN_CLUSTER") != "" || IsProbePodRunning(ctx))
	if useProbe {
		out, runErr := execInProbe(ctx, buildProbeHTTPResponderCmd(port, marker))
		if runErr != nil {
			return fmt.Errorf("starting HTTP responder on :%d: %w (probe stdout: %q)", port, runErr, out)
		}
		if strings.TrimSpace(out) != "200" {
			return fmt.Errorf("HTTP responder on :%d did not return 200 (probe stdout: %q)", port, out)
		}
		return nil
	}

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

// StopHTTPResponder kills any python3 http.server on port via the IAP tunnel or in-cluster probe pod.
func StopHTTPResponder(ctx context.Context, opts ProbeOptions, port int) error {
	useProbe := os.Getenv("GKEBNKCTL_FORCE_IAP") != "1" && (os.Getenv("GKEBNKCTL_PROBE_IN_CLUSTER") != "" || IsProbePodRunning(ctx))
	if useProbe {
		_, _ = execInProbe(ctx, buildProbeHTTPResponderStopCmd(port))
		return nil
	}

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
