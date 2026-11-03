package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeIssue(linter, text, filename string, line int) issue {
	var i issue
	i.FromLinter, i.Text = linter, text
	i.Pos.Filename, i.Pos.Line, i.Pos.Column = filename, line, 1
	return i
}

func writeFixture(t *testing.T, source string, issues []issue) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct{ Issues []issue }{issues})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "report.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestComplexityPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		old, new int
		want     int
	}{
		{"crosses threshold", 29, 32, 1},
		{"unchanged legacy", 32, 32, 0},
		{"worsening legacy", 32, 33, 1},
		{"improving legacy", 35, 32, 0},
		{"drops below threshold", 32, 29, 0},
		{"clean", 28, 29, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			makeIssues := func(n int, line int) []issue {
				if n < 30 {
					return nil
				}
				return []issue{makeIssue("gocyclo", fmt.Sprintf("cyclomatic complexity %d of func `Heavy` is high (> 30)", n), "sample.go", line)}
			}
			base, before := writeFixture(t, "package p\nfunc Heavy() {}\n", makeIssues(tc.old, 2))
			candidate, after := writeFixture(t, "package p\n\n// unrelated line shift\nfunc Heavy() { println(1) }\n", makeIssues(tc.new, 4))
			got, err := compare(base, before, candidate, after)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings=%v err=%v, want %d findings", got, err, tc.want)
			}
		})
	}
}

func TestDeclarationDiagnostics(t *testing.T) {
	t.Parallel()
	for _, linter := range []string{"paralleltest", "musttag", "gosec"} {
		t.Run(linter, func(t *testing.T) {
			t.Parallel()
			i := makeIssue(linter, "declaration finding", "sample.go", 2)
			base, before := writeFixture(t, "package p\nfunc TestSame() {}\n", nil)
			candidate, after := writeFixture(t, "package p\nfunc TestSame() { println(1) }\n", []issue{i})
			got, err := compare(base, before, candidate, after)
			if err != nil || len(got) != 1 {
				t.Fatalf("body-only new finding: %v, %v", got, err)
			}
			base, before = writeFixture(t, "package p\nfunc TestSame() {}\n", []issue{i})
			i.Pos.Line = 4
			i.Text = "declaration   finding"
			candidate, after = writeFixture(t, "package p\n\n// shifted\nfunc TestSame() { println(1) }\n", []issue{i})
			got, err = compare(base, before, candidate, after)
			if err != nil || len(got) != 0 {
				t.Fatalf("unchanged shifted finding: %v, %v", got, err)
			}
			candidate, after = writeFixture(t, "package p\n\n// shifted\nfunc TestOther() {}\n", []issue{i})
			got, err = compare(base, before, candidate, after)
			if err != nil || len(got) != 1 {
				t.Fatalf("other function must not inherit allowance: %v, %v", got, err)
			}
		})
	}
}

func TestMultiplicityAndMessageIdentity(t *testing.T) {
	t.Parallel()
	i := makeIssue("gosec", "G101: potential credential", "sample.go", 2)
	base, before := writeFixture(t, "package p\nfunc F() {}\n", []issue{i})
	candidate, after := writeFixture(t, "package p\nfunc F() {}\n", []issue{i, i})
	got, err := compare(base, before, candidate, after)
	if err != nil || len(got) != 1 {
		t.Fatalf("duplicate finding: %v %v", got, err)
	}
	i.Text = "G104: unhandled error"
	candidate, after = writeFixture(t, "package p\nfunc F() {}\n", []issue{i})
	got, err = compare(base, before, candidate, after)
	if err != nil || len(got) != 1 {
		t.Fatalf("new message: %v %v", got, err)
	}
}

func TestIdentity(t *testing.T) {
	t.Parallel()
	source := `package p

type A[T any] struct{}
type B struct{}
func (a *A[T]) Same() {}
func (b B) Same() {}
type (
 C struct {
  Field string
 }
)
var (
 One = 1
 Two = 2
)
`
	root, _ := writeFixture(t, source, nil)
	file, err := parseSource(filepath.Join(root, "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line int
		want string
	}{{5, "func:A.Same"}, {6, "func:B.Same"}, {9, "type:C"}, {13, "var:One"}, {14, "var:Two"}, {1, "file:package p"}} {
		got, err := file.identity(tc.line)
		if err != nil || got != tc.want {
			t.Errorf("line %d: got %q, %v want %q", tc.line, got, err, tc.want)
		}
	}
}

func TestAbsolutePathsAndFileIdentity(t *testing.T) {
	t.Parallel()
	i := makeIssue("musttag", "missing tag", "sample.go", 2)
	base, before := writeFixture(t, "package p\ntype A struct { X int }\n", []issue{i})
	candidate, after := writeFixture(t, "package p\ntype A struct { X int }\n", nil)
	i.Pos.Filename = filepath.Join(candidate, "sample.go")
	data, _ := json.Marshal(struct{ Issues []issue }{[]issue{i}})
	if err := os.WriteFile(after, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := compare(base, before, candidate, after)
	if err != nil || len(got) != 0 {
		t.Fatalf("absolute paths normalize: %v %v", got, err)
	}
	i.Pos.Filename = "other.go"
	if err := os.Rename(filepath.Join(candidate, "sample.go"), filepath.Join(candidate, "other.go")); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(struct{ Issues []issue }{[]issue{i}})
	if err := os.WriteFile(after, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = compare(base, before, candidate, after)
	if err != nil || len(got) != 1 {
		t.Fatalf("other file must not inherit allowance: %v %v", got, err)
	}
}

func TestInvalidReportsFailClosed(t *testing.T) {
	t.Parallel()
	for _, data := range []string{"", "{}", "null", `{"Issues":[]} {}`, `{"Issues":"bad"}`} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			root, path := writeFixture(t, "package p\n", nil)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := diagnostics(root, path); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
	for _, i := range []issue{
		makeIssue("gocyclo", "unknown complexity format", "sample.go", 2),
		makeIssue("typecheck", "compiler error", "sample.go", 2),
		makeIssue("musttag", "tag", "../outside.go", 2),
		makeIssue("musttag", "tag", "sample.go", 99),
		makeIssue("musttag", "tag", "missing.go", 2),
	} {
		root, path := writeFixture(t, "package p\nfunc F() {}\n", []issue{i})
		if _, err := diagnostics(root, path); err == nil {
			t.Fatalf("bad diagnostic accepted: %+v", i)
		}
	}
}

func TestMalformedSourceFailsClosed(t *testing.T) {
	t.Parallel()
	root, path := writeFixture(t, "package p\nfunc (\n", []issue{makeIssue("gosec", "finding", "sample.go", 2)})
	if _, err := diagnostics(root, path); err == nil || !strings.Contains(err.Error(), "sample.go") {
		t.Fatalf("malformed source accepted: %v", err)
	}
}
