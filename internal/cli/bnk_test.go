package cli

import (
	"context"
	"testing"
)

func resetBnkResyncFlags() {
	flagBnkResyncNamespace = ""
	flagBnkResyncAllInNS = false
	flagBnkResyncGatewayClass = ""
	flagBnkResyncDryRun = false
	flagBnkResyncKubeconfig = ""
	flagBnkResyncConfig = ""
	flagBnkResyncWatch = false
}

func TestResolveBnkResyncOpts_PositionalRoute(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncNamespace = "f5-bnk"
	flagBnkResyncDryRun = true

	opts, err := resolveBnkResyncOpts([]string{"my-route"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Namespace != "f5-bnk" || opts.Name != "my-route" {
		t.Errorf("expected f5-bnk/my-route, got %s/%s", opts.Namespace, opts.Name)
	}
	if !opts.DryRun {
		t.Error("expected DryRun=true")
	}
}

func TestResolveBnkResyncOpts_PositionalRoute_MissingNamespace(t *testing.T) {
	resetBnkResyncFlags()

	_, err := resolveBnkResyncOpts([]string{"my-route"})
	if err == nil {
		t.Fatal("expected error when namespace is missing for named route, got nil")
	}
}

func TestResolveBnkResyncOpts_AllInNS(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncNamespace = "test-ns"
	flagBnkResyncAllInNS = true

	opts, err := resolveBnkResyncOpts(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Namespace != "test-ns" || !opts.AllInNamespace {
		t.Errorf("expected test-ns with AllInNamespace=true, got %+v", opts)
	}
}

func TestResolveBnkResyncOpts_AllInNS_MissingNamespace(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncAllInNS = true

	_, err := resolveBnkResyncOpts(nil)
	if err == nil {
		t.Fatal("expected error when namespace is missing with --all-in-ns, got nil")
	}
}

func TestResolveBnkResyncOpts_GatewayClass(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncGatewayClass = "f5-bnk"

	opts, err := resolveBnkResyncOpts(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.GatewayClass != "f5-bnk" {
		t.Errorf("expected GatewayClass=f5-bnk, got %s", opts.GatewayClass)
	}
}

func TestResolveBnkResyncOpts_MultipleSelectors(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncNamespace = "f5-bnk"
	flagBnkResyncAllInNS = true
	flagBnkResyncGatewayClass = "f5-bnk"

	_, err := resolveBnkResyncOpts([]string{"my-route"})
	if err == nil {
		t.Fatal("expected error with multiple selectors, got nil")
	}
}

func TestResolveBnkResyncOpts_NoSelectors(t *testing.T) {
	resetBnkResyncFlags()

	_, err := resolveBnkResyncOpts(nil)
	if err == nil {
		t.Fatal("expected error with no selectors, got nil")
	}
}

func TestBuildBnkDynamicClient_ExplicitInvalidKubeconfig(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncKubeconfig = "/nonexistent/kubeconfig/path"
	flagBnkResyncConfig = ""

	_, err := buildBnkDynamicClient(context.Background())
	if err == nil {
		t.Fatal("expected error with invalid kubeconfig path, got nil")
	}
}

func TestBuildBnkDynamicClient_InvalidConfigFile(t *testing.T) {
	resetBnkResyncFlags()
	flagBnkResyncKubeconfig = ""
	flagBnkResyncConfig = "/nonexistent/cluster.yaml"

	_, err := buildBnkDynamicClient(context.Background())
	if err == nil {
		t.Fatal("expected error with nonexistent config file, got nil")
	}
}
