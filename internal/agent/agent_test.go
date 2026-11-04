package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTasklessOnlyJSON(t *testing.T) {
	for _, only := range []bool{false, true} {
		data, err := json.Marshal(Agent{Name: "coder", TasklessOnly: only})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"taskless_only":true`) != only {
			t.Fatalf("metadata serialization: %s", data)
		}
		if !only && strings.Contains(string(data), "taskless_only") {
			t.Fatalf("unexpected metadata on ordinary agent: %s", data)
		}
	}
}
