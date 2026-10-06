package jumphost

// BuildCurlCmd exposes buildCurlCmd for unit tests in the external _test package.
var BuildCurlCmd = buildCurlCmd

// BuildCurlBodyCmd exposes buildCurlBodyCmd for unit tests in the external _test package.
var BuildCurlBodyCmd = buildCurlBodyCmd

// BuildHTTPResponderCmd exposes buildHTTPResponderCmd for unit tests.
var BuildHTTPResponderCmd = buildHTTPResponderCmd

// BuildHTTPResponderStopCmd exposes buildHTTPResponderStopCmd for unit tests.
var BuildHTTPResponderStopCmd = buildHTTPResponderStopCmd

// Seam overrides — tests swap these to avoid real ssh/gcloud subprocess calls.

// PrepareIAPKeyFn is the overridable seam for prepareIAPKey consumed by
// RunStagingCommands and CopyFileViaIAP. Tests replace it to assert
// mint-once behaviour without network.
var PrepareIAPKeyFn = &prepareIAPKeyFn

// SSHRunViaIAPFn is the overridable seam for SSHRunViaIAP consumed by
// RunStagingCommands and CopyFileViaIAP. Tests replace it to capture
// commands without network.
var SSHRunViaIAPFn = &sshRunViaIAPFn

// SSHRunViaIAPStdinFn is the overridable seam for SSHRunViaIAPWithStdin
// consumed by RunStagingCommandWithStdin and CopyFileViaIAPStdin. Tests
// replace it to capture commands + stdin payloads without network.
var SSHRunViaIAPStdinFn = &sshRunViaIAPStdinFn

// PushSSHPublicKeyFn is the overridable seam for PushSSHPublicKey consumed by
// RunStagingCommands and CopyFileViaIAP. Tests replace it to assert
// re-push-per-step behaviour without network.
var PushSSHPublicKeyFn = &pushSSHPublicKeyFn
