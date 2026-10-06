package jumphost

// preflight.go — served-model preflight for aiperf runs.
//
// Before running aiperf, query GET <vip>/v1/models from the jumphost to obtain
// the model IDs that vLLM actually serves. If the requested model name is not
// among them, fail fast with a clear error that lists the served IDs so the
// operator can correct --model (or --served-model-name in vLLM config).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// v1ModelsResponse is the minimal subset of the OpenAI-compatible /v1/models response.
type v1ModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// buildModelsQueryCmd constructs the remote shell command that runs a curl
// against GET http://<vip>/v1/models (with optional Host header).
func buildModelsQueryCmd(vip, hostHeader string) string {
	curlArgs := []string{
		"curl", "-sf",
		shellSingleQuote(fmt.Sprintf("http://%s/v1/models", vip)),
	}
	if hostHeader != "" {
		curlArgs = append(curlArgs, "-H", shellSingleQuote(fmt.Sprintf("Host: %s", hostHeader)))
	}
	return strings.Join(curlArgs, " ")
}

// CheckServedModel queries GET <vip>/v1/models from the jumphost and verifies
// that requestedModel is among the served model IDs.
func CheckServedModel(ctx context.Context, probOpts ProbeOptions, requestedModel string) error {
	if requestedModel == "" || probOpts.VIP == "" {
		return nil
	}
	if probOpts.User == "" {
		probOpts.User = DefaultSSHUser
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, probOpts.Region, probOpts.InstanceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[preflight] could not prepare IAP key for /v1/models check: %v — proceeding\n", err)
		return nil
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, probOpts.Region, probOpts.InstanceID, pubKeyPath)

	remoteCmd := buildModelsQueryCmd(probOpts.VIP, probOpts.Hostname)

	stdout, sshErr := aiperfSSHExecFn(ctx, probOpts.Region, probOpts.InstanceID, keyPath, remoteCmd)
	if sshErr != nil {
		fmt.Fprintf(os.Stderr, "[preflight] /v1/models query failed (non-fatal, proceeding): %v\n", sshErr)
		return nil
	}

	jsonStart := strings.Index(stdout, "{")
	if jsonStart < 0 {
		fmt.Fprintf(os.Stderr, "[preflight] /v1/models returned non-JSON output (non-fatal, proceeding): %.200s\n", stdout)
		return nil
	}

	var resp v1ModelsResponse
	if parseErr := json.Unmarshal([]byte(stdout[jsonStart:]), &resp); parseErr != nil {
		fmt.Fprintf(os.Stderr, "[preflight] could not parse /v1/models response (non-fatal, proceeding): %v\n", parseErr)
		return nil
	}

	servedIDs := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		servedIDs = append(servedIDs, m.ID)
	}

	if len(servedIDs) == 0 {
		fmt.Fprintf(os.Stderr, "[preflight] /v1/models returned empty data list (non-fatal, proceeding)\n")
		return nil
	}

	for _, id := range servedIDs {
		if id == requestedModel {
			return nil
		}
	}

	hint := ""
	if len(servedIDs) == 1 {
		hint = fmt.Sprintf(" Try: --model %q", servedIDs[0])
	}
	return fmt.Errorf(
		"--model %q is not served by the endpoint; served models: %v."+
			" vLLM uses --served-model-name (not the HF repo path) to set the served id.%s",
		requestedModel, servedIDs, hint,
	)
}
