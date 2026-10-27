package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// managedFields captured from the demo Deployment after four live changes:
// kubectl scale, kubectl set resources with a custom field manager, kubectl
// edit, and kubectl set env (which adds an env var Git doesn't declare).
const driftedDeployment = `{
  "metadata": {"managedFields": [
    {"manager": "argocd-controller", "operation": "Update", "time": "2026-10-19T18:22:57Z",
     "fieldsV1": {"f:spec": {"f:template": {"f:spec": {"f:containers": {"k:{\"name\":\"podinfo\"}": {"f:image": {}}}}}}}},
    {"manager": "kubectl", "operation": "Update", "subresource": "scale",
     "fieldsV1": {"f:spec": {"f:replicas": {}}}},
    {"manager": "legacy-tuner", "operation": "Update", "time": "2026-10-26T22:49:59Z",
     "fieldsV1": {"f:spec": {"f:template": {"f:spec": {"f:containers": {"k:{\"name\":\"podinfo\"}": {"f:resources": {"f:limits": {"f:memory": {}}}}}}}}}},
    {"manager": "kubectl-edit", "operation": "Update", "time": "2026-10-26T22:50:00Z",
     "fieldsV1": {"f:spec": {"f:template": {"f:spec": {"f:containers": {"k:{\"name\":\"podinfo\"}": {"f:resources": {"f:limits": {"f:cpu": {}}}}}}}}}},
    {"manager": "kubectl-set", "operation": "Update", "time": "2026-10-26T22:50:17Z",
     "fieldsV1": {"f:spec": {"f:template": {"f:spec": {"f:containers": {"k:{\"name\":\"podinfo\"}": {"f:env": {".": {}, "k:{\"name\":\"DRIFT_TEST\"}": {".": {}, "f:name": {}, "f:value": {}}}}}}}}}},
    {"manager": "kube-controller-manager", "operation": "Update", "subresource": "status", "time": "2026-10-26T22:50:35Z",
     "fieldsV1": {"f:status": {"f:replicas": {}}}}
  ]},
  "spec": {"replicas": 3, "template": {"spec": {"containers": [
    {"name": "podinfo", "image": "ghcr.io/stefanprodan/podinfo:6.15.0",
     "env": [{"name": "DRIFT_TEST", "value": "1"}],
     "resources": {"limits": {"cpu": "250m", "memory": "160Mi"}}}
  ]}}}
}`

func TestLiveChanges(t *testing.T) {
	changes, err := liveChanges("Deployment/demo-app", []byte(driftedDeployment))
	if err != nil {
		t.Fatal(err)
	}

	const container = "spec.template.spec.containers[name=podinfo]"
	want := []struct {
		field, value, manager string
		hasTime, byPerson     bool
	}{
		// ArgoCD's and the controller's fields are left out; the rest are
		// sorted by field.
		{"spec.replicas", "3", "kubectl", false, true}, // scale records no time
		{container + ".env[name=DRIFT_TEST]", `{"name":"DRIFT_TEST","value":"1"}`, "kubectl-set", true, true},
		{container + ".resources.limits.cpu", `"250m"`, "kubectl-edit", true, true},
		{container + ".resources.limits.memory", `"160Mi"`, "legacy-tuner", true, false},
	}
	if len(changes) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(changes), len(want), changes)
	}
	for i, w := range want {
		c := changes[i]
		if c.Field != w.field || c.Value != w.value || c.Manager != w.manager ||
			(c.Time != nil) != w.hasTime || c.ByPerson() != w.byPerson {
			t.Errorf("change %d = %+v (by person %t); want %+v", i, c, c.ByPerson(), w)
		}
		if c.Resource != "Deployment/demo-app" {
			t.Errorf("change %d resource = %q", i, c.Resource)
		}
	}
	if changes[0].Subresource != "scale" {
		t.Errorf("scale change subresource = %q, want scale", changes[0].Subresource)
	}

	if _, err := liveChanges("x", []byte("not json")); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestLiveChangeDescribe(t *testing.T) {
	ts := time.Date(2026, 10, 26, 22, 50, 0, 0, time.UTC)
	now := ts.Add(4 * time.Minute)

	edit := LiveChange{Field: "spec.replicas", Value: "3", Manager: "kubectl-edit", Time: &ts}
	if got := edit.Describe(now); !strings.Contains(got, "4m ago") || !strings.Contains(got, "most likely a person") {
		t.Errorf("Describe() = %q", got)
	}
	scale := LiveChange{Field: "spec.replicas", Value: "3", Manager: "kubectl", Subresource: "scale"}
	if got := scale.Describe(now); !strings.Contains(got, "via the scale subresource") || !strings.Contains(got, "time not recorded") {
		t.Errorf("Describe() = %q", got)
	}
	script := LiveChange{Field: "spec.replicas", Value: "3", Manager: "legacy-tuner", Time: &ts}
	if got := script.Describe(now); !strings.Contains(got, "automation") {
		t.Errorf("Describe() = %q", got)
	}
}

func TestOwnedFields(t *testing.T) {
	live := map[string]any{
		"metadata": map[string]any{
			"labels":     map[string]any{"team": "sre"},
			"finalizers": []any{"example.com/cleanup"},
		},
		"spec": map[string]any{"containers": []any{
			map[string]any{"name": "podinfo", "image": "podinfo:6.15.0", "ports": []any{
				map[string]any{"containerPort": float64(9898), "protocol": "TCP"},
			}},
		}},
	}
	tests := []struct {
		name   string
		fields string // fieldsV1 JSON
		want   []ownedField
	}{
		{"plain field", `{"f:spec":{"f:containers":{"k:{\"name\":\"podinfo\"}":{"f:image":{}}}}}`,
			[]ownedField{{"spec.containers[name=podinfo].image", "podinfo:6.15.0"}}},
		{"list item picked by two keys, sorted", `{"f:spec":{"f:containers":{"k:{\"name\":\"podinfo\"}":{"f:ports":{"k:{\"protocol\":\"TCP\",\"containerPort\":9898}":{".":{},"f:containerPort":{}}}}}}}`,
			[]ownedField{{"spec.containers[name=podinfo].ports[containerPort=9898,protocol=TCP]", map[string]any{"containerPort": float64(9898), "protocol": "TCP"}}}},
		{"set member", `{"f:metadata":{"f:finalizers":{".":{},"v:\"example.com/cleanup\"":{}}}}`,
			[]ownedField{{`metadata.finalizers["example.com/cleanup"]`, json.RawMessage(`"example.com/cleanup"`)}}},
		{"node owned with no subfields", `{"f:metadata":{"f:labels":{".":{}}}}`,
			[]ownedField{{"metadata.labels", map[string]any{"team": "sre"}}}},
		{"owned field missing from the live object", `{"f:spec":{"f:replicas":{}}}`,
			[]ownedField{{"spec.replicas", nil}}},
		{"unrecognized keys are skipped", `{"i:0":{},".":{}}`, nil},
	}
	for _, tc := range tests {
		var fields map[string]any
		if err := json.Unmarshal([]byte(tc.fields), &fields); err != nil {
			t.Fatalf("%s: bad test input: %v", tc.name, err)
		}
		if got := ownedFields(fields, "", live); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %#v\nwant %#v", tc.name, got, tc.want)
		}
	}
}

func TestFormatDriftReport(t *testing.T) {
	now := time.Date(2026, 10, 26, 23, 0, 0, 0, time.UTC)
	edited := now.Add(-4 * time.Minute)
	st := AppStatus{Sync: "OutOfSync", Health: "Healthy", Revision: "abc123", Resources: []ResourceStatus{
		{Kind: "Deployment", Name: "demo-app", Status: "OutOfSync"},
		{Kind: "Service", Name: "demo-app", Status: "Synced"},
	}}
	changes := []LiveChange{
		{Resource: "Deployment/demo-app", Field: "spec.replicas", Value: "3", Manager: "kubectl", Subresource: "scale"},
		{Resource: "Deployment/demo-app", Field: "spec.template.spec.containers[name=podinfo].resources.limits.memory",
			Value: `"256Mi"`, Manager: "kubectl-edit", Time: &edited},
	}
	diff := "===== apps/Deployment demo/demo-app ======\n<   replicas: 3\n---\n>   replicas: 2\n"

	got := formatDriftReport(st, changes, diff, now)
	for _, want := range []string{
		"sync=OutOfSync, health=Healthy, comparing against Git revision abc123",
		"  Deployment/demo-app: OutOfSync\n  Service/demo-app: Synced",
		"  Deployment/demo-app\n    spec.replicas = 3", // grouped under the resource, listed once
		"via the scale subresource, time not recorded",
		`limits.memory = "256Mi"`,
		"4m ago",
		"<   replicas: 3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "  Deployment/demo-app\n") != 1 {
		t.Errorf("resource header should appear once:\n%s", got)
	}

	empty := formatDriftReport(AppStatus{Sync: "Synced", Health: "Healthy"}, nil, "", now)
	for _, want := range []string{"none: every field is owned by ArgoCD", "no differences."} {
		if !strings.Contains(empty, want) {
			t.Errorf("empty report missing %q:\n%s", want, empty)
		}
	}
}
