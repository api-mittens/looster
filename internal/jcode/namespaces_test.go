package jcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/api-mittens/looster/internal/app"
)

func TestNamespaceSubprocessIdentityCannotBeInheritedOrOverridden(t *testing.T) {
	for _, name := range []string{"", "research"} {
		t.Run(name, func(t *testing.T) {
			selected := name
			if selected == "" {
				selected = "default"
			}
			dir := t.TempDir()
			binary := filepath.Join(dir, "jcode")
			script := "#!/bin/sh\n[ \"$LOOSTER_NAMESPACE\" = '" + selected + "' ] || exit 20\n[ \"$LOOSTER_ROLE\" = agent ] || exit 21\n[ \"$LOOSTER_AGENT\" = coder ] || exit 22\n[ -z \"${LOOSTER_TASK_ID+x}\" ] || exit 23\n[ \"$LOOSTER_JOB_ID\" = 42 ] || exit 24\n[ -z \"${LOOSTER_SESSION_ID+x}\" ] || exit 25\nprintf '%s\\n' '{\"type\":\"done\",\"session_id\":\"namespace_test\"}'\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "namespace_test.json"), []byte(`{"messages":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LOOSTER_NAMESPACE", "inherited_wrong")
			t.Setenv("LOOSTER_TASK_ID", "TSK-999")
			t.Setenv("LOOSTER_JOB_ID", "999")
			t.Setenv("LOOSTER_SESSION_ID", "inherited_wrong")
			harness := Harness{Binary: binary, SessionsDir: dir, Environment: map[string]string{"LOOSTER_NAMESPACE": "override_wrong", "LOOSTER_ROLE": "human", "LOOSTER_AGENT": "wrong", "LOOSTER_TASK_ID": "TSK-888", "LOOSTER_JOB_ID": "888", "LOOSTER_SESSION_ID": "override_wrong"}}
			_, err := harness.Run(context.Background(), app.Run{JobID: 42, Namespace: name, Agent: "coder", Prompt: "Work"}, func(app.Output) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
