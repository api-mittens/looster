package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownLicenseFailsClosed(t *testing.T) {
	for _, name := range []string{"LICENSE", "nested/COPYING", "licenses/custom.txt", "web/static/vendor/D3-LICENSE.txt", "web/static/vendor/D3-THIRD-PARTY-LICENSES.txt"} {
		var out bytes.Buffer
		text := "GNU GENERAL PUBLIC LICENSE Version 3"
		if filepath.Base(name) == "D3-THIRD-PARTY-LICENSES.txt" {
			text = "--- LICENSE (verbatim from tarball) ---\n" + text
		}
		if err := emit(&out, notice{name, text}); err == nil || !strings.Contains(err.Error(), "explicit license review required") {
			t.Errorf("%s: expected review failure, got %v", name, err)
		}
		if out.Len() != 0 {
			t.Fatal("failed license partially rendered")
		}
	}
}

func TestSupplementalLabels(t *testing.T) {
	for _, name := range []string{"NOTICE", "AUTHORS", "COPYRIGHT", "PATENTS", "NOTICE.txt", "license-check.yml", "pgproto3/notice_response.go"} {
		kind, err := noticeClassification(notice{name, "supplemental information"})
		if err != nil || !strings.HasPrefix(kind, "Supplemental") {
			t.Errorf("%s: %q %v", name, kind, err)
		}
	}
}

func TestFullyIdentifiedMixedLicenseAllowed(t *testing.T) {
	kind, err := noticeClassification(notice{"LICENSE", `Permission is hereby granted, free of charge. THE SOFTWARE IS PROVIDED "AS IS". Apache License Version 2.0`})
	if err != nil || !strings.HasPrefix(kind, "Mixed text: MIT, Apache-2.0") {
		t.Fatalf("%q %v", kind, err)
	}
}

func TestD3CollectionCannotMaskUnknownDocument(t *testing.T) {
	mit := "Permission is hereby granted, free of charge. THE SOFTWARE IS PROVIDED 'AS IS'"
	separator := "\n===================================================================\n"
	text := "--- LICENSE (verbatim from tarball) ---\n" + mit + separator + "another-package@1.0.0" + separator + "--- LICENSE (verbatim from tarball) ---\nunknown custom terms"
	if _, err := noticeClassification(notice{"D3-THIRD-PARTY-LICENSES.txt", text}); err == nil {
		t.Fatal("known MIT block masked unknown second license")
	}
	text = strings.Replace(text, "unknown custom terms", "Apache License Version 2.0", 1)
	if _, err := noticeClassification(notice{"D3-THIRD-PARTY-LICENSES.txt", text}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckedInD3Collection(t *testing.T) {
	root, err := rootDir()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "web/static/vendor/D3-THIRD-PARTY-LICENSES.txt"))
	if err != nil {
		t.Fatal(err)
	}
	kind, err := noticeClassification(notice{"D3-THIRD-PARTY-LICENSES.txt", string(b)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MIT", "ISC", "BSD-3-Clause", "Apache-2.0", "Unlicense"} {
		if !strings.Contains(kind, want) {
			t.Errorf("missing %s from %q", want, kind)
		}
	}
}

func TestCheckedInMarkdownCollection(t *testing.T) {
	root, err := rootDir()
	if err != nil {
		t.Fatal(err)
	}
	const name = "MARKDOWN-IT-THIRD-PARTY-LICENSES.txt"
	b, err := os.ReadFile(filepath.Join(root, "web/static/vendor", name))
	if err != nil {
		t.Fatal(err)
	}
	kind, err := noticeClassification(notice{name, string(b)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MIT", "BSD-2-Clause"} {
		if !strings.Contains(kind, want) {
			t.Errorf("missing %s from %q", want, kind)
		}
	}
	malicious := string(b) + "\n===================================================================\n--- LICENSE (verbatim from tarball) ---\nunknown custom terms"
	if _, err := noticeClassification(notice{name, malicious}); err == nil {
		t.Fatal("known licenses masked an unrecognized appended document")
	}
}

func TestUnlicenseContentMatch(t *testing.T) {
	text := "This is free and unencumbered software released into the public domain. Anyone is free to copy, modify, publish, use, compile, sell, or distribute. http://unlicense.org"
	kind, err := noticeClassification(notice{"LICENSE", text})
	if err != nil || !strings.HasPrefix(kind, "Unlicense") {
		t.Fatalf("%q %v", kind, err)
	}
}

func TestReviewedLucentExactBytesOnly(t *testing.T) {
	const lucent = `Copyright (C) 1998, 1999 by Lucent Technologies
All Rights Reserved

Permission to use, copy, modify, and distribute this software and
its documentation for any purpose and without fee is hereby
granted, provided that the above copyright notice appear in all
copies and that both that the copyright notice and this
permission notice and warranty disclaimer appear in supporting
documentation, and that the name of Lucent or any of its entities
not be used in advertising or publicity pertaining to
distribution of the software without specific, written prior
permission.

LUCENT DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS SOFTWARE,
INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS.
IN NO EVENT SHALL LUCENT OR ANY OF ITS ENTITIES BE LIABLE FOR ANY
SPECIAL, INDIRECT OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER
IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION,
ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
`
	kind, err := noticeClassification(notice{"ftoa/LICENSE_LUCENE", lucent})
	if err != nil || !strings.Contains(kind, "Reviewed Lucent permissive") || !strings.Contains(kind, reviewedLucentSHA256) {
		t.Fatalf("%q %v", kind, err)
	}
	for _, text := range []string{lucent + "\n", strings.Replace(lucent, "without fee", "with a fee", 1), strings.ReplaceAll(lucent, "\n", "\r\n")} {
		if _, err := noticeClassification(notice{"ftoa/LICENSE_LUCENE", text}); err == nil {
			t.Fatal("modified Lucent license accepted")
		}
	}
}

func TestModuleLicenseRequired(t *testing.T) {
	for _, ns := range [][]notice{nil, {{"NOTICE", "notice"}}, {{"license-check.yml", "workflow"}}, {{"license.go", "package example"}}, {{"LICENSE", " \n"}}, {{"nested/LICENSE", "nested license only"}}} {
		if hasModuleLicense(ns) {
			t.Errorf("accepted missing root license: %#v", ns)
		}
	}
	if !hasModuleLicense([]notice{{"LICENSE.txt", "actual license"}}) {
		t.Fatal("rejected root license")
	}
}

func TestWriteOrCheck(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "notices.md")
	if err := writeOrCheck(dest, []byte("new"), true); err == nil {
		t.Fatal("accepted missing output")
	}
	if err := writeOrCheck(dest, []byte("old"), false); err != nil {
		t.Fatal(err)
	}
	if err := writeOrCheck(dest, []byte("old"), true); err != nil {
		t.Fatal(err)
	}
	if err := writeOrCheck(dest, []byte("new"), true); err == nil {
		t.Fatal("accepted stale output")
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "old" {
		t.Fatal("check mutated output")
	}
}

func TestWrappedISCAndTypographicMIT(t *testing.T) {
	if got := classify("Permission to use, copy, modify, and/or distribute this software for any purpose\nwith or without fee"); !strings.HasPrefix(got, "ISC") {
		t.Fatal(got)
	}
	if got := classify("Permission is hereby granted, free of charge. THE SOFTWARE IS PROVIDED “AS IS”"); !strings.HasPrefix(got, "MIT") {
		t.Fatal(got)
	}
}

func TestCollectRecursiveCompleteAndSorted(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"LICENSE": "full root license\n", "nested/COPYING.txt": "copying\nall lines\n", "nested/deeper/AUTHORS": "author\n", "nested/NOTICE.md": "notice", "COPYRIGHT": "copyright", "nested/license.go": "// license fixture", "licenses/BSD-2-Clause.txt": "nested named license\n", "ignored.txt": "not a notice"}
	for name, text := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ns, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != len(files)-1 {
		t.Fatalf("got %d notices", len(ns))
	}
	last := ""
	for _, n := range ns {
		if n.name <= last {
			t.Fatal("not sorted")
		}
		last = n.name
		if n.text != files[n.name] {
			t.Errorf("text changed: %s", n.name)
		}
		if filepath.IsAbs(n.name) {
			t.Fatal("absolute path")
		}
	}
}
func TestMissingDirectory(t *testing.T) {
	if _, err := collect(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing directory error")
	}
}
func TestMatch(t *testing.T) {
	for _, n := range []string{"LICENSE", "LICENSE-MIT", "LICENCE.txt", "COPYING.LESSER", "NOTICE", "AUTHORS.md", "copyright", "PATENTS"} {
		if !match(n) {
			t.Error(n)
		}
	}
	for _, n := range []string{"main.go", "licensed.go", "unnoticed.go"} {
		if match(n) {
			t.Error(n)
		}
	}
}
func TestClassify(t *testing.T) {
	mit := `Permission is hereby granted, free of charge. THE SOFTWARE IS PROVIDED "AS IS"`
	apache := "Apache License Version 2.0"
	for _, tc := range []struct{ text, want string }{{mit, "MIT"}, {apache, "Apache-2.0"}, {"Mozilla Public License Version 2.0", "MPL-2.0"}, {"Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee", "ISC"}, {mit + apache, "Mixed text:"}, {"GNU GENERAL PUBLIC LICENSE Version 3", "UNKNOWN"}, {"Copyright 2026 someone", "UNKNOWN"}} {
		if got := classify(tc.text); !strings.HasPrefix(got, tc.want) {
			t.Errorf("classify(%q)=%q, want %s", tc.text, got, tc.want)
		}
	}
}
func TestProxyEscape(t *testing.T) {
	if got := escape("github.com/SomeOrg/Thing/v2"); got != "github.com/!some!org/!thing/v2" {
		t.Fatal(got)
	}
}
func TestEmitPreservesTextAndEscapesFence(t *testing.T) {
	var out bytes.Buffer
	text := "copyright\n```\nall terms\n"
	if err := emit(&out, notice{"nested/NOTICE", text}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "````text\n"+text+"````\n") {
		t.Fatal(out.String())
	}
}
func TestSortedKeys(t *testing.T) {
	if got := strings.Join(sortedKeys(map[string]bool{"z": true, "a": false}), ","); got != "a,z" {
		t.Fatal(got)
	}
}
