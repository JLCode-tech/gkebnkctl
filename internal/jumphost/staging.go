package jumphost

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var interfaceFlagRegex = regexp.MustCompile(`--interface\s+\S+\s*`)

func stripInterfaceFlag(cmd string) string {
	return interfaceFlagRegex.ReplaceAllString(cmd, "")
}

func shouldUseProbe(ctx context.Context) bool {
	if os.Getenv("GKEBNKCTL_FORCE_IAP") == "1" {
		return false
	}
	if os.Getenv("GKEBNKCTL_PROBE_IN_CLUSTER") != "" {
		return true
	}
	return IsProbePodRunning(ctx)
}

// Package-level function-variable seams consumed ONLY by the staging helpers
// (RunStagingCommands, RunStagingCommandWithStdin, CopyFileViaIAP,
// CopyFileViaIAPStdin). Default to the real implementations. Tests override
// these via export_test.go to assert command sequencing without network.
var (
	prepareIAPKeyFn        = prepareIAPKey
	sshRunViaIAPFn         = SSHRunViaIAP
	sshRunViaIAPStdinFn    = SSHRunViaIAPWithStdin
	pushSSHPublicKeyFn     = PushSSHPublicKey
	execInProbeFn          = execInProbe
	execInProbeWithStdinFn = execInProbeWithStdin
)

// RunStagingCommands mints+pushes an ephemeral IAP key internally, then runs each command in order over SSH-via-IAP.
// When an in-cluster probe pod is available (and GKEBNKCTL_FORCE_IAP is not 1), it executes commands via execInProbe.
func RunStagingCommands(ctx context.Context, opts ProbeOptions, commands []string) ([]string, error) {
	if shouldUseProbe(ctx) {
		out := make([]string, 0, len(commands))
		for _, cmd := range commands {
			cleanedCmd := stripInterfaceFlag(cmd)
			stdout, runErr := execInProbeFn(ctx, cleanedCmd)
			out = append(out, stdout)
			if runErr != nil {
				return out, runErr
			}
		}
		return out, nil
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out := make([]string, 0, len(commands))
	for _, cmd := range commands {
		_ = pushSSHPublicKeyFn(ctx, opts.Region, opts.InstanceID, pubKeyPath)

		stdout, runErr := sshRunViaIAPFn(ctx, opts.Region, opts.InstanceID, keyPath, cmd)
		out = append(out, stdout)
		if runErr != nil {
			return out, runErr
		}
	}
	return out, nil
}

// RunStagingCommandWithStdin runs a SINGLE command over SSH-via-IAP with stdin attached,
// or via execInProbeWithStdin when using an in-cluster probe pod.
func RunStagingCommandWithStdin(ctx context.Context, opts ProbeOptions, command string, stdin io.Reader) (string, error) {
	if shouldUseProbe(ctx) {
		cleanedCmd := stripInterfaceFlag(command)
		return execInProbeWithStdinFn(ctx, cleanedCmd, stdin)
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return "", err
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, opts.Region, opts.InstanceID, pubKeyPath)

	return sshRunViaIAPStdinFn(ctx, opts.Region, opts.InstanceID, keyPath, command, stdin)
}

// CopyFileViaIAP writes content to remotePath on the jumphost by base64-encoding content.
func CopyFileViaIAP(ctx context.Context, opts ProbeOptions, content []byte, remotePath string) error {
	if shouldUseProbe(ctx) {
		b64 := base64.StdEncoding.EncodeToString(content)
		dir := filepath.Dir(remotePath)
		remoteCmd := fmt.Sprintf(
			"mkdir -p %s && printf '%%s' %s | base64 -d > %s",
			shellSingleQuote(dir),
			shellSingleQuote(b64),
			shellSingleQuote(remotePath),
		)
		_, runErr := execInProbeFn(ctx, remoteCmd)
		if runErr != nil {
			return fmt.Errorf("copy %s: %w", remotePath, runErr)
		}
		return nil
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return err
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, opts.Region, opts.InstanceID, pubKeyPath)

	b64 := base64.StdEncoding.EncodeToString(content)
	dir := filepath.Dir(remotePath)

	remoteCmd := fmt.Sprintf(
		"mkdir -p %s && printf '%%s' %s | base64 -d > %s",
		shellSingleQuote(dir),
		shellSingleQuote(b64),
		shellSingleQuote(remotePath),
	)

	_, runErr := sshRunViaIAPFn(ctx, opts.Region, opts.InstanceID, keyPath, remoteCmd)
	if runErr != nil {
		return fmt.Errorf("copy %s: %w", remotePath, runErr)
	}
	return nil
}

// CopyFileViaIAPStdin writes content to remotePath on the jumphost by piping the bytes through stdin.
func CopyFileViaIAPStdin(ctx context.Context, opts ProbeOptions, content []byte, remotePath string) error {
	if shouldUseProbe(ctx) {
		dir := filepath.Dir(remotePath)
		remoteCmd := fmt.Sprintf(
			"mkdir -p %s && cat > %s",
			shellSingleQuote(dir),
			shellSingleQuote(remotePath),
		)
		_, runErr := execInProbeWithStdinFn(ctx, remoteCmd, bytes.NewReader(content))
		if runErr != nil {
			return fmt.Errorf("copy %s: %w", remotePath, runErr)
		}
		return nil
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return err
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, opts.Region, opts.InstanceID, pubKeyPath)

	dir := filepath.Dir(remotePath)
	remoteCmd := fmt.Sprintf(
		"mkdir -p %s && cat > %s",
		shellSingleQuote(dir),
		shellSingleQuote(remotePath),
	)

	_, runErr := sshRunViaIAPStdinFn(ctx, opts.Region, opts.InstanceID, keyPath, remoteCmd, bytes.NewReader(content))
	if runErr != nil {
		return fmt.Errorf("copy %s: %w", remotePath, runErr)
	}
	return nil
}

// ShellSingleQuote wraps s in single quotes safe for /bin/sh.
func ShellSingleQuote(s string) string {
	return shellSingleQuote(s)
}

// GrpcurlInstallCmd returns the idempotent remote shell command that installs grpcurl.
func GrpcurlInstallCmd() string {
	const releaseURL = "https://github.com/fullstorydev/grpcurl/releases/download/v1.9.3/grpcurl_1.9.3_linux_x86_64.tar.gz"
	return fmt.Sprintf(
		`command -v grpcurl >/dev/null 2>&1 || (curl -sSL %s | sudo tar -xz -C /usr/local/bin grpcurl); grpcurl --version`,
		releaseURL,
	)
}
