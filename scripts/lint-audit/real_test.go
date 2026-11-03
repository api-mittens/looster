package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var realLinter = flag.String("real-linter", "", "optional pinned golangci-lint binary for integration tests")

func TestRealLinterReports(t *testing.T) {
	if *realLinter == "" {
		t.Skip("pass -real-linter=/absolute/path/to/golangci-lint to exercise real reports")
	}
	complexitySource := func(n int) string {
		return "package fixture\nfunc Heavy(b bool) int {\n n := 0\n" + strings.Repeat(" if b { n++ }\n", n-1) + " return n\n}\n"
	}
	const parallel = "package fixture\nimport \"testing\"\nfunc TestSame(t *testing.T) {\n%s\n t.Log(\"same declaration\")\n}\n"
	const musttag = "package fixture\nimport \"encoding/json\"\ntype Payload struct { Value string }\nfunc Encode() ([]byte, error) { return json.Marshal(%s) }\n"
	const gosec = "package fixture\nimport \"os\"\nfunc Permission(path string) error {\n return os.Chmod(path, %s)\n}\n"
	for _, tc := range []struct {
		name, filename, before, after, linter string
		want                                  int
	}{
		{"complexity crossing unchanged declaration", "sample.go", complexitySource(29), complexitySource(32), "gocyclo", 1},
		{"complexity worsening legacy", "sample.go", complexitySource(31), complexitySource(32), "gocyclo", 1},
		{"complexity improving legacy", "sample.go", complexitySource(34), complexitySource(32), "gocyclo", 0},
		{"complexity shifted legacy", "sample.go", complexitySource(32), strings.Replace(complexitySource(32), "package fixture\n", "package fixture\n\n// shift\n", 1), "gocyclo", 0},
		{"paralleltest removed parallel body", "sample_test.go", fmt.Sprintf(parallel, "t.Parallel()"), fmt.Sprintf(parallel, ""), "paralleltest", 1},
		{"paralleltest shifted legacy", "sample_test.go", fmt.Sprintf(parallel, ""), "// shift\n\n" + fmt.Sprintf(parallel, ""), "paralleltest", 0},
		{"musttag new use of unchanged type", "sample.go", fmt.Sprintf(musttag, "map[string]string{}"), fmt.Sprintf(musttag, "Payload{}"), "musttag", 1},
		{"musttag shifted legacy", "sample.go", fmt.Sprintf(musttag, "Payload{}"), "// shift\n\n" + fmt.Sprintf(musttag, "Payload{}"), "musttag", 0},
		{"gosec body change", "sample.go", fmt.Sprintf(gosec, "0600"), fmt.Sprintf(gosec, "0777"), "gosec", 1},
		{"gosec shifted legacy", "sample.go", fmt.Sprintf(gosec, "0777"), "// shift\n\n" + fmt.Sprintf(gosec, "0777"), "gosec", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			makeReport := func(source string) (string, string) {
				t.Helper()
				root := t.TempDir()
				files := map[string]string{
					tc.filename:     source,
					"go.mod":        "module example.invalid/fixture\n\ngo 1.27.2\n",
					".golangci.yml": "version: \"2\"\nlinters:\n  default: none\n  settings:\n    gocyclo:\n      min-complexity: 30\n",
				}
				for name, contents := range files {
					if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(root, "report.json")
				cmd := exec.Command(*realLinter, "run", "--config=.golangci.yml", "--enable-only="+tc.linter,
					"--issues-exit-code=0", "--max-issues-per-linter=0", "--max-same-issues=0", "--uniq-by-line=false",
					"--show-stats=false", "--output.text.path=/dev/null", "--output.json.path="+path, "./...")
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("linter: %v\n%s", err, output)
				}
				return root, path
			}
			base, before := makeReport(tc.before)
			candidate, after := makeReport(tc.after)
			got, err := compare(base, before, candidate, after)
			if err != nil || len(got) != tc.want {
				data, _ := os.ReadFile(after)
				t.Fatalf("findings=%v err=%v, want %d findings\nreport: %s", got, err, tc.want, data)
			}
		})
	}
}
