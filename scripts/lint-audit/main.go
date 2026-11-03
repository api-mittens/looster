// Command lint-audit compares declaration-aware golangci-lint JSON reports.
// It deliberately lives in a separate, standard-library-only module so the
// bootstrap tool is not itself input to the application's lint or test pass.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type issue struct {
	FromLinter string
	Text       string
	Pos        struct {
		Filename string
		Line     int
		Column   int
	}
}

type report struct {
	Issues *[]issue
}

type diagnostic struct {
	key        string
	complexity int
	issue      issue
}

var complexityPattern = regexp.MustCompile(`^(cyclomatic complexity )([0-9]+)( of func .*)$`)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: lint-audit BASE_TREE BASE_JSON CANDIDATE_TREE CANDIDATE_JSON")
		os.Exit(2)
	}
	findings, err := compare(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
	if err != nil {
		fmt.Fprintln(os.Stderr, "pre-commit: declaration audit:", err)
		os.Exit(2)
	}
	for _, finding := range findings {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: %s (%s, declaration audit)\n", finding.Pos.Filename, finding.Pos.Line, finding.Pos.Column, finding.Text, finding.FromLinter)
	}
	if len(findings) != 0 {
		os.Exit(1)
	}
}

func compare(baseTree, baseJSON, candidateTree, candidateJSON string) ([]issue, error) {
	baseline, err := diagnostics(baseTree, baseJSON)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := diagnostics(candidateTree, candidateJSON)
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}
	// Preserve multiplicity: one old finding cannot hide two candidate findings
	// in the same declaration. Pair largest complexities first so an improvement
	// is allowed, but a higher maximum can never be hidden by a second function.
	old := make(map[string][]int)
	for _, d := range baseline {
		old[d.key] = append(old[d.key], d.complexity)
	}
	for key := range old {
		sort.Sort(sort.Reverse(sort.IntSlice(old[key])))
	}
	sort.SliceStable(candidate, func(i, j int) bool { return candidate[i].complexity > candidate[j].complexity })
	var findings []issue
	for _, d := range candidate {
		previous := old[d.key]
		if len(previous) == 0 {
			findings = append(findings, d.issue)
			continue
		}
		if d.complexity > previous[0] {
			findings = append(findings, d.issue)
		}
		old[d.key] = previous[1:]
	}
	return findings, nil
}

func diagnostics(root, path string) ([]diagnostic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r report
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&r); err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("report has trailing data")
	}
	// Issues:null is the linter's representation of a clean report. Require the
	// field itself so a truncated/incorrect report cannot silently pass.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if _, ok := fields["Issues"]; !ok {
		return nil, fmt.Errorf("report missing Issues field")
	}
	if r.Issues == nil {
		return nil, nil
	}
	files := make(map[string]*sourceFile)
	var result []diagnostic
	for _, i := range *r.Issues {
		switch i.FromLinter {
		case "gocyclo", "paralleltest", "musttag", "gosec":
		default:
			return nil, fmt.Errorf("unexpected audit linter %q", i.FromLinter)
		}
		name := filepath.Clean(i.Pos.Filename)
		if filepath.IsAbs(name) {
			name, err = filepath.Rel(root, name)
			if err != nil {
				return nil, err
			}
		}
		if name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("diagnostic outside snapshot: %q", i.Pos.Filename)
		}
		file := files[name]
		if file == nil {
			file, err = parseSource(filepath.Join(root, name))
			if err != nil {
				return nil, err
			}
			files[name] = file
		}
		identity, err := file.identity(i.Pos.Line)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		text := strings.Join(strings.Fields(i.Text), " ")
		complexity := 0
		if i.FromLinter == "gocyclo" {
			match := complexityPattern.FindStringSubmatch(text)
			if match == nil {
				return nil, fmt.Errorf("unrecognized gocyclo diagnostic: %q", text)
			}
			complexity, err = strconv.Atoi(match[2])
			if err != nil {
				return nil, err
			}
			// The AST already identifies the function. Do not let the linter's
			// rendering of pointer/generic receivers change that identity.
			text = "cyclomatic complexity"
		}
		i.Pos.Filename = filepath.ToSlash(name)
		result = append(result, diagnostic{
			key:        strings.Join([]string{i.FromLinter, i.Pos.Filename, identity, text}, "\x00"),
			complexity: complexity,
			issue:      i,
		})
	}
	return result, nil
}

type sourceFile struct {
	set   *token.FileSet
	file  *ast.File
	lines []string
}

func parseSource(path string) (*sourceFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, data, 0)
	if err != nil {
		return nil, err
	}
	return &sourceFile{set: set, file: file, lines: strings.Split(string(data), "\n")}, nil
}

func (s *sourceFile) contains(node ast.Node, line int) bool {
	return s.set.Position(node.Pos()).Line <= line && line <= s.set.Position(node.End()).Line
}

func (s *sourceFile) identity(line int) (string, error) {
	if line < 1 || line > len(s.lines) {
		return "", fmt.Errorf("invalid diagnostic line %d", line)
	}
	for _, decl := range s.file.Decls {
		if !s.contains(decl, line) {
			continue
		}
		switch d := decl.(type) {
		case *ast.FuncDecl:
			receiver := ""
			if d.Recv != nil && len(d.Recv.List) != 0 {
				receiver = receiverName(d.Recv.List[0].Type) + "."
			}
			return "func:" + receiver + d.Name.Name, nil
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if !s.contains(spec, line) {
					continue
				}
				switch v := spec.(type) {
				case *ast.TypeSpec:
					return "type:" + v.Name.Name, nil
				case *ast.ValueSpec:
					names := make([]string, len(v.Names))
					for n, name := range v.Names {
						names[n] = name.Name
					}
					return d.Tok.String() + ":" + strings.Join(names, ","), nil
				}
			}
		}
	}
	// Imports, package declarations, and diagnostics on comments have no named
	// declaration. Source text, rather than line number, survives line shifts.
	return "file:" + strings.Join(strings.Fields(s.lines[line-1]), " "), nil
}

func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.IndexExpr:
		return receiverName(e.X)
	case *ast.IndexListExpr:
		return receiverName(e.X)
	default:
		return "?"
	}
}
