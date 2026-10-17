package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAppStatus(t *testing.T) {
	// Seven deploys in history; only the last five should be kept.
	var history []string
	for i := 1; i <= 7; i++ {
		history = append(history, fmt.Sprintf(`{"revision":"rev%d","deployedAt":"t%d"}`, i, i))
	}
	data := `{"status":{
		"sync":{"status":"Synced","revision":"rev7"},
		"health":{"status":"Degraded","message":"Deployment has exceeded its progress deadline"},
		"history":[` + strings.Join(history, ",") + `]}}`

	st, err := parseAppStatus([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if st.Sync != "Synced" || st.Revision != "rev7" || st.Health != "Degraded" {
		t.Errorf("got sync=%q revision=%q health=%q", st.Sync, st.Revision, st.Health)
	}
	if !strings.Contains(st.Message, "progress deadline") {
		t.Errorf("health message not parsed: %q", st.Message)
	}
	if len(st.History) != 5 || st.History[0].Revision != "rev3" || st.History[4].Revision != "rev7" {
		t.Errorf("history = %+v; want rev3..rev7", st.History)
	}

	if _, err := parseAppStatus([]byte("not json")); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}
