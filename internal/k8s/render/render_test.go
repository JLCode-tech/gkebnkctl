package render

import (
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
)

func TestRender(t *testing.T) {
	tmpl := []byte("name: {{ .Name }}\nmtu: {{ .MTU }}\n")
	out, err := Render(tmpl, struct {
		Name string
		MTU  int
	}{Name: "tmm-ext", MTU: 1460})
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "name: tmm-ext") {
		t.Errorf("output missing rendered Name: %q", got)
	}
	if !strings.Contains(got, "mtu: 1460") {
		t.Errorf("output missing rendered MTU: %q", got)
	}
}

func TestRender_ParseError(t *testing.T) {
	_, err := Render([]byte("{{ .Unclosed"), nil)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestRender_ExecuteError(t *testing.T) {
	// text/template errors when a field is accessed on a value that doesn't
	// have it (here: .Bar on a string field).
	_, err := Render([]byte("{{ .Foo.Bar }}"), struct{ Foo string }{Foo: "x"})
	if err == nil {
		t.Fatal("expected execute error, got nil")
	}
}

func TestRenderCNEInstance_AIGateway241(t *testing.T) {
	tmplBytes, err := manifests.FS.ReadFile("templates/cneinstance.yaml.tmpl")
	if err != nil {
		t.Fatalf("ReadFile cneinstance.yaml.tmpl: %v", err)
	}
	out, err := RenderCNEInstance(tmplBytes, CNEInstanceVars{
		ManifestVersion: "2.4.1-3.2850.1-0.0.9",
		TmmMtu:          "1460",
	})
	if err != nil {
		t.Fatalf("RenderCNEInstance 2.4.1: %v", err)
	}
	rendered := string(out)
	for _, want := range []string{
		"aiGateway:",
		"enabled: true",
		"aiMetrics: true",
		"loggingSubsystem:\n      enabled: true",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered CNEInstance 2.4.1 missing %q:\n%s", want, rendered)
		}
	}

	for _, v := range []string{"2.4.1", "v2.4.1-3.2850.1-0.0.9", "2.4.3", "2.5.0", "3.0.0"} {
		if !isAIGatewayManifestVersion(v) {
			t.Errorf("expected isAIGatewayManifestVersion(%q) = true", v)
		}
	}
	for _, v := range []string{"", "2.3.0", "2.4.0", "2.4.0-3.2882.0", "v2.4.0"} {
		if isAIGatewayManifestVersion(v) {
			t.Errorf("expected isAIGatewayManifestVersion(%q) = false", v)
		}
	}
}
