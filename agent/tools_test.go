package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
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

func TestDiagnosisSchemaMatchesStruct(t *testing.T) {
	// The model fills in submit_diagnosis using the schema; the loop decodes it
	// into Diagnosis. If a field is renamed in one but not the other, it would
	// silently decode as empty, so check they list the same fields.
	schema := diagnosisToolParam().InputSchema
	props := schema.Properties.(map[string]any)

	tags := map[string]bool{}
	typ := reflect.TypeOf(Diagnosis{})
	for i := 0; i < typ.NumField(); i++ {
		tags[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}

	if len(props) != len(tags) || len(schema.Required) != len(tags) {
		t.Fatalf("schema has %d properties and %d required; Diagnosis has %d fields",
			len(props), len(schema.Required), len(tags))
	}
	for _, name := range schema.Required {
		if !tags[name] {
			t.Errorf("schema field %q has no matching Diagnosis field", name)
		}
		if _, ok := props[name]; !ok {
			t.Errorf("required field %q missing from schema properties", name)
		}
	}
}

func TestRunTool(t *testing.T) {
	audit, err := OpenAuditLog(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()

	long := strings.Repeat("x", maxToolOutput+100)
	tools := map[string]Tool{
		"ok":   {Run: func(context.Context, json.RawMessage) (string, error) { return "fine", nil }},
		"fail": {Run: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("boom") }},
		"long": {Run: func(context.Context, json.RawMessage) (string, error) { return long, nil }},
	}

	tests := []struct {
		tool      string
		wantError bool
		wantText  string
	}{
		{"ok", false, "fine"},
		{"fail", true, "boom"},            // tool errors go back to the model, not crash the run
		{"missing", true, "unknown tool"}, // the model asked for a tool that doesn't exist
		{"long", false, "[output truncated]"},
	}
	for _, tc := range tests {
		block := runTool(context.Background(), tools, "id", tc.tool, json.RawMessage(`{}`), 1, audit, io.Discard)
		b, _ := json.Marshal(block)
		got := string(b)
		if strings.Contains(got, `"is_error":true`) != tc.wantError {
			t.Errorf("%s: is_error mismatch in %s", tc.tool, got)
		}
		if !strings.Contains(got, tc.wantText) {
			t.Errorf("%s: result missing %q", tc.tool, tc.wantText)
		}
	}
}
