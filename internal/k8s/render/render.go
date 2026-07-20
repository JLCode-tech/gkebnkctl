// Package render provides Go text/template rendering for the BNK manifest
// templates embedded in internal/k8s/manifests. Templates use {{ .Field }}
// syntax (converted from the bash seed's ${VAR} envsubst placeholders — see
// docs/SPEC-go-tracer.md §5 for the per-file mapping table).
//
// Task A defines only the base Render function. Task C/D append one Vars
// struct + one Render<Thing> wrapper per template (nadsVars/RenderNADs,
// cneInstanceVars/RenderCNEInstance, vlansRoutesVars/RenderVlansRoutes,
// licenseCRVars/RenderLicenseCR, egressVars/RenderEgress,
// agentRouteSetterVars/RenderAgentRouteSetter, discoverPodVars/
// RenderDiscoverPod) — purely additive to this file, per the append-only
// seam called out in docs/SPEC-go-tracer.md §8 merge notes.
package render

import (
	"bytes"
	"fmt"
	"text/template"
)

// Render executes a Go text/template given in tmpl with data as the
// dot-value and returns the rendered bytes. Returns a descriptive error on
// any parse or execution failure.
func Render(tmpl []byte, data interface{}) ([]byte, error) {
	t, err := template.New("manifest").Parse(string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("render: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render: execute template: %w", err)
	}
	return buf.Bytes(), nil
}
