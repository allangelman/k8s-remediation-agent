package agent

import (
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
