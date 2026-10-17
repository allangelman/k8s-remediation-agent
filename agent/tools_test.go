package agent

import (
	"encoding/json"
	"testing"
)

func TestParseName(t *testing.T) {
	valid := []string{"demo-app", "demo-app-66d66dd598-xz8q5", "a"}
	for _, name := range valid {
		in, _ := json.Marshal(map[string]string{"name": name})
		if got, err := parseName(in); err != nil || got != name {
			t.Errorf("parseName(%q) = %q, %v; want %q, nil", name, got, err, name)
		}
	}

	// Anything that isn't a plain Kubernetes name must be rejected, so the
	// model can't pass flags, paths, or other namespaces to kubectl.
	invalid := []string{"", "--all-namespaces", "-n=kube-system", "pod/demo", "Demo-App", "demo app", "demo-app-", "../etc"}
	for _, name := range invalid {
		in, _ := json.Marshal(map[string]string{"name": name})
		if _, err := parseName(in); err == nil {
			t.Errorf("parseName(%q) succeeded; want error", name)
		}
	}
}
