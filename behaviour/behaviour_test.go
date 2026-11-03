package behaviour

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		n         int
		bad       bool
	}{
		{"empty", "-- disabled\n", 0, false},
		{"rules", "ON TODO PING coder -- dispatch\nAFTER coder COMPLETED PING reviewer", 2, false},
		{"duplicate", "ON TODO PING coder\nON TODO PING coder", 1, false},
		{"malformed", "ON RUNNING PING coder", 0, true},
		{"case sensitive", "on todo ping coder", 0, true},
		{"invalid agent", "AFTER ../coder COMPLETED PING reviewer", 0, true},
		{"double dash agent name", "ON TODO PING coder--backup -- comment", 1, false},
		{"semicolon batch", "ON TODO PING coder; AFTER coder COMPLETED PING reviewer;", 2, false},
		{"comments on later lines", "ON TODO PING coder;\n\n-- later comment; \"ignored\"\nEVERY 10 MINUTES\n PING coder WITH \"triage; report\";", 2, false},
		{"mixed separators", "ON TODO PING coder;\nAFTER coder COMPLETED PING reviewer\nEVERY 1 HOUR PING coder WITH \"review\";", 3, false},
		{"multiline statements", "EVERY 10\nMINUTES PING coder\nWITH \"review\";\nON TODO\nPING coder;", 2, false},
		{"empty statements", ";;\n; -- ignored; \"\n", 0, false},
		{"comment after semicolon", "ON TODO PING coder;-- ignore AFTER coder COMPLETED PING unknown;\nAFTER coder COMPLETED PING reviewer;", 2, false},
		{"quoted semicolon", `EVERY 1 HOUR PING coder WITH "review; report -- blockers"; ON TODO PING coder;`, 2, false},
		{"duplicate across separators", "ON TODO PING coder;\nON TODO PING coder;", 1, false},
		{"invalid second statement", "ON TODO PING coder; ON WRONG PING coder;", 0, true},
		{"unterminated quoted semicolon", `EVERY 1 HOUR PING coder WITH "review; ON TODO PING coder;`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := Parse(tc.src)
			if (e != nil) != tc.bad {
				t.Fatalf("error=%v", e)
			}
			if e == nil && len(r) != tc.n {
				t.Fatalf("got %d rules", len(r))
			}
		})
	}
}

func TestBatchPreservesPromptsAndErrorLocations(t *testing.T) {
	src := `EVERY 10 MINUTES PING coder WITH "First; say \"ready; now\" -- then stop"; EVERY 1 HOUR PING reviewer WITH "Second; review";`
	rules, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Prompt != `First; say "ready; now" -- then stop` || rules[1].Prompt != "Second; review" {
		t.Fatalf("prompts changed: %+v", rules)
	}
	bad := "-- header;\nON TODO PING coder;\n\n  EVERY 0 MINUTES\n  PING coder WITH \"bad\";"
	if rules, err := Parse(bad); err == nil || rules != nil || !strings.Contains(err.Error(), "line 4:") {
		t.Fatalf("batch not rejected at its starting line: rules=%v error=%v", rules, err)
	}
}

func TestRecurringRules(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		seconds   int64
		prompt    string
		bad       bool
	}{
		{"minutes", `EVERY 10 MINUTES PING AGENT_3 WITH "Prompt ...."`, 600, "Prompt ....", false},
		{"singular", `EVERY 1 SECOND PING AGENT_3 WITH "tick"`, 1, "tick", false},
		{"hours", `EVERY 2 HOURS PING AGENT_3 WITH "review"`, 7200, "review", false},
		{"days", `EVERY 1 DAY PING AGENT_3 WITH "daily"`, 86400, "daily", false},
		{"quoted comment", `EVERY 10 MINUTES PING AGENT_3 WITH "Run --check and report" -- outside comment`, 600, "Run --check and report", false},
		{"escaped quotes", `EVERY 1 MINUTE PING AGENT_3 WITH "Say \"ready\" -- then stop"`, 60, `Say "ready" -- then stop`, false},
		{"escaped slash and newline", `EVERY 1 MINUTE PING AGENT_3 WITH "C:\\work\nnext"`, 60, "C:\\work\nnext", false},
		{"unicode", `EVERY 1 HOUR PING AGENT_3 WITH "Check \u00e6"`, 3600, "Check æ", false},
		{"zero", `EVERY 0 MINUTES PING AGENT_3 WITH "x"`, 0, "", true},
		{"negative", `EVERY -1 MINUTES PING AGENT_3 WITH "x"`, 0, "", true},
		{"fractional", `EVERY 0.5 MINUTES PING AGENT_3 WITH "x"`, 0, "", true},
		{"overflow", `EVERY 9223372036854775808 MINUTES PING AGENT_3 WITH "x"`, 0, "", true},
		{"duration overflow", `EVERY 9223372036 DAYS PING AGENT_3 WITH "x"`, 0, "", true},
		{"bad unit", `EVERY 1 WEEKS PING AGENT_3 WITH "x"`, 0, "", true},
		{"unquoted", `EVERY 1 MINUTE PING AGENT_3 WITH x`, 0, "", true},
		{"unfinished quote", `EVERY 1 MINUTE PING AGENT_3 WITH "x`, 0, "", true},
		{"invalid escape", `EVERY 1 MINUTE PING AGENT_3 WITH "\q"`, 0, "", true},
		{"empty prompt", `EVERY 1 MINUTE PING AGENT_3 WITH ""`, 0, "", true},
		{"whitespace prompt", `EVERY 1 MINUTE PING AGENT_3 WITH " \t "`, 0, "", true},
		{"invalid target", `EVERY 1 MINUTE PING ../agent WITH "x"`, 0, "", true},
		{"trailing garbage", `EVERY 1 MINUTE PING AGENT_3 WITH "x" extra`, 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := Parse(tc.src)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if tc.bad {
				return
			}
			if len(rules) != 1 {
				t.Fatalf("rules=%v", rules)
			}
			r := rules[0]
			if r.Trigger != "every" || r.Target != "AGENT_3" || r.IntervalSeconds != tc.seconds || r.Prompt != tc.prompt {
				t.Fatalf("unexpected rule %+v", r)
			}
		})
	}
	duplicate := "EVERY 1 HOUR PING coder WITH \"review\"\nEVERY 60 MINUTES PING coder WITH \"review\""
	if rules, err := Parse(duplicate); err != nil || len(rules) != 1 {
		t.Fatalf("equivalent rules not deduplicated: %v %v", rules, err)
	}
	if _, err := Parse("ON TODO PING coder\nEVERY 0 MINUTES PING coder WITH \"x\""); err == nil || !strings.Contains(err.Error(), "line 2:") {
		t.Fatalf("missing line number: %v", err)
	}
}

func TestAgentDefinitionsAndStatusRules(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      Rule
		bad       bool
	}{
		{"agent definition", `PROMPT planner WITH "Investigate tasks; ask questions -- preserve quotes."`, Rule{Trigger: "prompt", Target: "planner", Prompt: "Investigate tasks; ask questions -- preserve quotes."}, false},
		{"planning", `ON PLANNING PING planner`, Rule{Trigger: "planning", Target: "planner"}, false},
		{"unvalidated removed", `ON UNVALIDATED PING reviewer`, Rule{}, true},
		{"validated", `ON VALIDATED PING publisher`, Rule{Trigger: "validated", Target: "publisher"}, false},
		{"closed", `ON CLOSED PING publisher`, Rule{Trigger: "closed", Target: "publisher"}, false},
		{"short after", `AFTER coder PING reviewer`, Rule{Trigger: "completed", Source: "coder", Target: "reviewer"}, false},
		{"stop after verified", `AFTER VERIFIED STOP PINGING coder`, Rule{Trigger: "stop_validated", Target: "coder"}, false},
		{"stop after todo", `AFTER TODO STOP PINGING coder`, Rule{Trigger: "stop_todo", Target: "coder"}, false},
		{"invalid stop target", `AFTER VERIFIED STOP PINGING ../coder`, Rule{}, true},
		{"empty agent prompt", `PROMPT coder WITH ""`, Rule{}, true},
		{"invalid agent", `PROMPT ../coder WITH "Implement"`, Rule{}, true},
		{"unquoted instructions", `PROMPT coder WITH Implement`, Rule{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, e := Parse(tc.src)
			if (e != nil) != tc.bad {
				t.Fatalf("error=%v", e)
			}
			if !tc.bad && (len(rules) != 1 || rules[0] != tc.want) {
				t.Fatalf("unexpected statements: %+v", rules)
			}
		})
	}
	for _, tc := range []struct {
		name, src string
		n         int
		bad       bool
	}{
		{"consistent duplicates", `PROMPT coder WITH "Implement"; PROMPT coder WITH "Implement";`, 1, false},
		{"conflicting definitions", `PROMPT coder WITH "Implement"; PROMPT coder WITH "Review";`, 0, true},
		{"after aliases deduplicate", `AFTER coder PING reviewer; AFTER coder COMPLETED PING reviewer;`, 1, false},
		{"verified aliases deduplicate", `AFTER VERIFIED STOP PINGING coder; AFTER VALIDATED STOP PINGING coder;`, 1, false},
		{"definitions may follow rules", `ON PLANNING PING planner; PROMPT planner WITH "Investigate";`, 2, false},
		{"example layout missing semicolon", `PROMPT reviewer WITH "Review code."
EVERY 1 HOURS PING reviewer WITH "Inspect open work.";
ON PLANNING PING reviewer;
AFTER coder PING reviewer;`, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := Parse(tc.src)
			if (e != nil) != tc.bad {
				t.Fatalf("error=%v", e)
			}
			if !tc.bad && len(r) != tc.n {
				t.Fatalf("statements=%v", r)
			}
		})
	}
}
