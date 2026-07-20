package render

import (
	"strings"
	"testing"
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
