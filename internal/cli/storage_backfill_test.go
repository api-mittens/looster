package cli

import (
	"io"
	"strings"
	"testing"
)

func TestStorageMaintenanceSafety(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"backfill"}, "--confirm-readers-upgraded"},
		{[]string{"backfill", "--confirm-readers-upgraded", "--limit", "0"}, "must be positive"},
		{[]string{"verify", "--limit", "-1"}, "must not be negative"},
	} {
		a := &application{}
		cmd := a.storageMaintenanceCommand()
		cmd.SetArgs(tc.args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
	a := &application{}
	root := a.storageMaintenanceCommand()
	for _, sub := range root.Commands() {
		if sub.Annotations["human"] != "true" {
			t.Fatalf("%s not human-only", sub.Name())
		}
		if sub.Annotations["local"] == "true" {
			t.Fatalf("%s bypasses mandatory storage/fence startup", sub.Name())
		}
	}
}
