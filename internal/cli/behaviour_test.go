package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/behaviour"
)

type behaviourRecorder struct {
	app.Repository
	calls     int
	source    string
	activated int64
}

func (r *behaviourRecorder) ApplyManifest(_ context.Context, source string) (behaviour.Manifest, error) {
	r.calls++
	r.source = source
	rules, err := behaviour.Parse(source)
	return behaviour.Manifest{ID: 2, Source: source, Rules: rules, Active: true}, err
}
func (r *behaviourRecorder) Manifests(context.Context) ([]behaviour.Manifest, error) {
	return []behaviour.Manifest{{ID: 2, Source: r.source, Active: true}}, nil
}
func (r *behaviourRecorder) ActivateManifest(_ context.Context, id int64) error {
	r.calls++
	r.activated = id
	return nil
}

func TestBehaviourBatchInput(t *testing.T) {
	const source = `-- Exact original formatting matters.
AGENT coder WITH "Implement tasks; provide test evidence.";
TAG team/backend WITH "Backend tasks";
ON TODO PING coder WHEN TAGGED team/*;
AFTER coder COMPLETED PING reviewer WHEN NOT TAGGED archived;
EVERY 10 MINUTES PING coder WITH "Triage; report -- blockers";
`
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		bad   bool
		check bool
	}{
		{"apply stdin", []string{"apply"}, source, false, false},
		{"apply positional file", []string{"apply", "@file"}, "invalid", false, false},
		{"apply flag file", []string{"apply", "--file", "@file"}, "", false, false},
		{"set alias full replacement", []string{"set", "-f", "@file"}, "", false, false},
		{"check file", []string{"check", "@file"}, "", false, true},
		{"stdin argument", []string{"apply", "-"}, source, false, false},
		{"stdin flag", []string{"apply", "-f", "-"}, source, false, false},
		{"empty clears", []string{"apply"}, "", false, false},
		{"invalid batch", []string{"apply"}, "ON TODO PING coder; EVERY 0 MINUTES PING coder WITH \"bad\";", true, false},
		{"conflicting sources", []string{"apply", "@file", "--file", "@file"}, "", true, false},
		{"missing file", []string{"apply", "@missing"}, source, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.looster")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{}, tc.args...)
			want := tc.stdin
			for i, arg := range args {
				if arg == "@file" {
					args[i] = path
					want = source
				}
				if arg == "@missing" {
					args[i] = path + ".missing"
				}
			}
			recorder := &behaviourRecorder{}
			a := &application{repo: recorder}
			cmd := a.behaviourCommand()
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetArgs(args)
			cmd.SetIn(strings.NewReader(tc.stdin))
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			err := cmd.ExecuteContext(context.Background())
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if tc.bad {
				if recorder.calls != 0 {
					t.Fatal("invalid batch reached persistence")
				}
				return
			}
			if tc.check {
				var rules []behaviour.Rule
				if err := json.Unmarshal(out.Bytes(), &rules); err != nil || len(rules) != 5 {
					t.Fatalf("check output %s: %v", out.String(), err)
				}
				if recorder.calls != 0 {
					t.Fatal("check wrote")
				}
			} else {
				if recorder.calls != 1 || recorder.source != want {
					t.Fatalf("original source lost: calls=%d source=%q", recorder.calls, recorder.source)
				}
			}
		})
	}
}

func TestBehaviourVersionCommands(t *testing.T) {
	for _, tc := range []struct {
		args []string
		bad  bool
		id   int64
	}{
		{[]string{"activate", "7"}, false, 7}, {[]string{"activate", "0"}, true, 0}, {[]string{"activate", "bad"}, true, 0}, {[]string{"list"}, false, 0},
	} {
		r := &behaviourRecorder{}
		cmd := (&application{repo: r}).behaviourCommand()
		cmd.SetArgs(tc.args)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		err := cmd.ExecuteContext(context.Background())
		if (err != nil) != tc.bad || r.activated != tc.id {
			t.Fatalf("%v: error=%v activated=%d", tc.args, err, r.activated)
		}
	}
}

func TestBehaviourRequiresManifestPort(t *testing.T) {
	cmd := (&application{}).behaviourCommand()
	cmd.SetArgs([]string{"apply"})
	cmd.SetIn(strings.NewReader(`ON TODO PING coder`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "versioned manifests") {
		t.Fatalf("missing port error=%v", err)
	}
}
