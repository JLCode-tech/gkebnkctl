package multivip

// This file exports internal constructors for use by the external _test package.
// It is compiled only during `go test`.

import "github.com/JLCode-tech/gkebnkctl/internal/scenarios"

// NewScenarioForTest returns a scenario with the supplied deps injected.
// Only for use in tests.
func NewScenarioForTest(d VerifyDeps) scenarios.Scenario {
	return &scenario{vDeps: &d}
}
