package k8s

import "testing"

func condObj(conds ...map[string]interface{}) map[string]interface{} {
	raw := make([]interface{}, len(conds))
	for i, c := range conds {
		raw[i] = c
	}
	return map[string]interface{}{"status": map[string]interface{}{"conditions": raw}}
}

func TestUnstructuredConditionIsTrue(t *testing.T) {
	obj := condObj(
		map[string]interface{}{"type": "Accepted", "status": "True"},
		map[string]interface{}{"type": "F5TmmAvailable", "status": "True"},
	)
	if !UnstructuredConditionIsTrue(obj, "F5TmmAvailable") {
		t.Error("F5TmmAvailable=True should read as true")
	}
}

func TestUnstructuredConditionIsTrue_False(t *testing.T) {
	obj := condObj(map[string]interface{}{"type": "F5TmmAvailable", "status": "False"})
	if UnstructuredConditionIsTrue(obj, "F5TmmAvailable") {
		t.Error("F5TmmAvailable=False must not read as true")
	}
}

func TestUnstructuredConditionIsTrue_Missing(t *testing.T) {
	obj := condObj(map[string]interface{}{"type": "Accepted", "status": "True"})
	if UnstructuredConditionIsTrue(obj, "F5TmmAvailable") {
		t.Error("a missing condition must read as false")
	}
	if UnstructuredConditionIsTrue(map[string]interface{}{}, "F5TmmAvailable") {
		t.Error("no status must read as false")
	}
}
