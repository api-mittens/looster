package behaviour

import (
	"reflect"
	"strings"
	"testing"
)

func TestManifestDSL(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   Rule
		bad    bool
	}{
		{`AGENT coder WITH "Use WHEN TAGGED literally; preserve -- comments"`, Rule{Trigger: "agent", Target: "coder", Prompt: "Use WHEN TAGGED literally; preserve -- comments"}, false},
		{`AGENT blank WITH ""`, Rule{Trigger: "agent", Target: "blank"}, false},
		{`TAG team/backend WITH "Backend work"`, Rule{Trigger: "tag", Target: "team/backend", Prompt: "Backend work"}, false},
		{`TAG team/backend WITH ""`, Rule{Trigger: "tag", Target: "team/backend"}, false},
		{`ON TODO PING coder WHEN TAGGED team/*`, Rule{Trigger: "todo", Target: "coder", TagPattern: "team/*"}, false},
		{"ON\tTODO\tPING coder\tWHEN\tTAGGED\tteam/*", Rule{Trigger: "todo", Target: "coder", TagPattern: "team/*"}, false},
		{"ON TODO PING coder\n-- condition follows\nWHEN NOT TAGGED team/*;", Rule{Trigger: "todo", Target: "coder", TagPattern: "team/*", TagNegated: true}, false},
		{`ON TODO WHEN NOT TAGGED team/* PING coder`, Rule{Trigger: "todo", Target: "coder", TagPattern: "team/*", TagNegated: true}, false},
		{`AFTER coder COMPLETED PING reviewer WHEN NOT TAGGED */skip`, Rule{Trigger: "completed", Source: "coder", Target: "reviewer", TagPattern: "*/skip", TagNegated: true}, false},
		{`AFTER coder WHEN TAGGED * PING reviewer`, Rule{Trigger: "completed", Source: "coder", Target: "reviewer", TagPattern: "*"}, false},
		{`AFTER VALIDATED STOP PINGING coder WHEN TAGGED team/*`, Rule{Trigger: "stop_validated", Target: "coder", TagPattern: "team/*"}, false},
		{`AFTER CLOSED WHEN NOT TAGGED team/* STOP PINGING coder`, Rule{Trigger: "stop_closed", Target: "coder", TagPattern: "team/*", TagNegated: true}, false},
		{`TAG team/backend`, Rule{}, true},
		{`TAG team/backend WITH missing-quotes`, Rule{}, true},
		{`TAG team_1 WITH "bad"`, Rule{}, true},
		{`ON TODO PING coder WHEN TAGGED %`, Rule{}, true},
		{`ON TODO PING coder WHEN TAGGED [abc]`, Rule{}, true},
		{`ON TODO PING coder WHEN NOT TAGGED`, Rule{}, true},
		{`ON TODO PING coder WHEN TAGGED a WHEN TAGGED b`, Rule{}, true},
		{`AGENT coder WITH "x" WHEN TAGGED team/*`, Rule{}, true},
		{`EVERY 1 HOUR PING coder WITH "x" WHEN TAGGED team/*`, Rule{}, true},
		{`TAG team WITH "x" WHEN TAGGED team/*`, Rule{}, true},
	} {
		t.Run(tc.source, func(t *testing.T) {
			rules, err := Parse(tc.source)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if !tc.bad && (len(rules) != 1 || rules[0] != tc.want) {
				t.Fatalf("got %+v want %+v", rules, tc.want)
			}
		})
	}
}

func TestManifestFormatRoundTrip(t *testing.T) {
	control := []Rule{{Trigger: "agent", Target: "coder", Prompt: "control \x01 \a \v"}}
	if got, err := Parse(Format(control)); err != nil || !reflect.DeepEqual(got, control) {
		t.Fatalf("JSON control escapes lost: %+v %v", got, err)
	}
	rules, err := Parse(`AGENT coder WITH "Escaped \"quote\"\nnewline"; TAG project WITH "Project"; ON TODO WHEN TAGGED project PING coder; AFTER coder PING reviewer WHEN NOT TAGGED hidden; AFTER CLOSED STOP PINGING coder WHEN TAGGED *; EVERY 1 HOUR PING coder WITH "tick";`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(Format(rules))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rules) {
		t.Fatalf("roundtrip changed rules: %+v != %+v", got, rules)
	}
	if _, err := Parse(`AGENT coder WITH "a"; PROMPT coder WITH "b";`); err == nil {
		t.Fatal("cross-alias conflict accepted")
	}
	if _, err := Parse("TAG project WITH \"a\";\nTAG project WITH \"b\";"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("tag conflict not located: %v", err)
	}
}
