package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AuditLog appends one JSON object per line for every step of a run, so a run
// can be replayed and reviewed after the fact.
type AuditLog struct {
	f *os.File
}

func OpenAuditLog(path string) (*AuditLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &AuditLog{f: f}, nil
}

func (a *AuditLog) Write(event string, fields map[string]any) error {
	rec := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "event": event}
	for k, v := range fields {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = a.f.Write(append(b, '\n'))
	return err
}

func (a *AuditLog) Close() error { return a.f.Close() }
