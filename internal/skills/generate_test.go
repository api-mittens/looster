package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		location  string
		existing  bool
		force     bool
		repeat    bool
		wantError bool
	}{
		{name: "default home"},
		{name: "custom root", location: "custom"},
		{name: "expanded home", location: "~/custom"},
		{name: "repeat is idempotent", location: "custom", repeat: true},
		{name: "preserve edits", location: "custom", existing: true, wantError: true},
		{name: "explicit replacement", location: "custom", existing: true, force: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := tc.location
			expectedRoot := filepath.Join(home, ".agents", "skills")
			if tc.location != "" {
				expectedRoot = filepath.Join(home, "custom")
				if tc.location == "custom" {
					dir = expectedRoot
				}
			}
			folder := filepath.Join(expectedRoot, "looster")
			path := filepath.Join(folder, "SKILL.md")
			if err := os.MkdirAll(folder, 0755); err != nil {
				t.Fatal(err)
			}
			unrelated := filepath.Join(folder, "notes.txt")
			if err := os.WriteFile(unrelated, []byte("local notes"), 0644); err != nil {
				t.Fatal(err)
			}
			seed := []byte("customized skill")
			if tc.existing {
				if err := os.WriteFile(path, seed, 0644); err != nil {
					t.Fatal(err)
				}
			}
			getHome := func() (string, error) { return home, nil }
			got, err := generate(dir, tc.force, getHome)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if !tc.wantError && got != path {
				t.Fatalf("path=%s, want %s", got, path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantError {
				if !bytes.Equal(data, seed) {
					t.Fatal("local edits were overwritten")
				}
			} else if len(data) == 0 || bytes.Equal(data, seed) {
				t.Fatal("skill not generated")
			}
			if tc.repeat {
				if _, err := generate(dir, false, getHome); err != nil {
					t.Fatal(err)
				}
				again, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, again) {
					t.Fatal("repeat changed the skill")
				}
			}
			if data, err := os.ReadFile(unrelated); err != nil || string(data) != "local notes" {
				t.Fatal("unrelated file changed")
			}
		})
	}
}

func TestGenerateRejectsSymlinks(t *testing.T) {
	for _, folderLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[folderLink], func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			target := filepath.Join(outside, "SKILL.md")
			if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(root, "looster")
			if folderLink {
				if err := os.Symlink(outside, folder); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(folder, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(folder, "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Generate(root, true); err == nil {
				t.Fatal("followed symlink")
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
				t.Fatal("symlink target changed")
			}
		})
	}
}
