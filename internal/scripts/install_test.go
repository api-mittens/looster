package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	for _, tc := range []struct {
		name, version, checksum, repo string
		fail                          bool
	}{
		{"pinned", "v1.2.3", "", "", false},
		{"latest", "", "", "", false},
		{"override pinned", "v1.2.3", "", "example/looster-fork", false},
		{"override latest", "", "", "example/looster-fork", false},
		{"bad checksum", "v1.2.3", strings.Repeat("0", 64), "", true},
		{"invalid version", "v1/../../bad", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fixture := filepath.Join(dir, "fixtures")
			tools := filepath.Join(dir, "tools")
			dest := filepath.Join(dir, "install path")
			for _, p := range []string{fixture, tools, dest} {
				if err := os.MkdirAll(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			payload := []byte("#!/bin/sh\nprintf 'fixture binary\\n'\n")
			realBinary := tc.name == "pinned" && os.Getenv("LOOSTER_TEST_BINARY") != ""
			if realBinary {
				var err error
				payload, err = os.ReadFile(os.Getenv("LOOSTER_TEST_BINARY"))
				if err != nil {
					t.Fatal(err)
				}
			}
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: "./looster", Mode: 0755, Size: int64(len(payload))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			name := "looster_v1.2.3_linux_amd64.tar.gz"
			checksum := fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
			if tc.checksum != "" {
				checksum = tc.checksum
			}
			write := func(path string, data []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, data, mode); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(fixture, name), buf.Bytes(), 0644)
			write(filepath.Join(fixture, "checksums.txt"), []byte(checksum+"  "+name+"\n"), 0644)
			write(filepath.Join(tools, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo x86_64;; esac\n"), 0755)
			write(filepath.Join(tools, "curl"), []byte(`#!/bin/sh
out=
url=
while [ "$#" -gt 0 ]; do
 case "$1" in
  -o) out=$2; shift 2;;
  --proto|--tlsv1.2|-w) if [ "$1" = --tlsv1.2 ]; then shift; else shift 2; fi;;
  https://*) url=$1; shift;;
  *) shift;;
 esac
done
printf '%s\n' "$url" >> "$REQUEST_LOG"
case "$url" in
 */releases/latest) printf '%s/tag/v1.2.3' "${url%/latest}";;
 *) cp "$FIXTURE/${url##*/}" "$out";;
esac
`), 0755)
			write(filepath.Join(dest, "looster"), []byte("previous binary"), 0755)
			cmd := exec.Command("sh", "../../scripts/install.sh")
			// Do not inherit a developer's override: default cases must exercise
			// the installer's own canonical repository, not one supplied by tests.
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "LOOSTER_REPO=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			requestLog := filepath.Join(dir, "requests")
			write(requestLog, nil, 0644)
			cmd.Env = append(cmd.Env, "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE="+fixture, "REQUEST_LOG="+requestLog, "LOOSTER_INSTALL_DIR="+dest, "LOOSTER_VERSION="+tc.version)
			if tc.repo != "" {
				cmd.Env = append(cmd.Env, "LOOSTER_REPO="+tc.repo)
			}
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v, output=%s", err, output)
			}
			requests, err := os.ReadFile(requestLog)
			if err != nil {
				t.Fatal(err)
			}
			repo := "api-mittens/looster"
			if tc.repo != "" {
				repo = tc.repo
			}
			base := "https://github.com/" + repo + "/releases/"
			wantRequests := ""
			if tc.version == "" {
				wantRequests = base + "latest\n"
			}
			if tc.name != "invalid version" {
				wantRequests += base + "download/v1.2.3/" + name + "\n" + base + "download/v1.2.3/checksums.txt\n"
			}
			if string(requests) != wantRequests {
				t.Fatalf("requested URLs = %q, want %q", requests, wantRequests)
			}
			got, err := os.ReadFile(filepath.Join(dest, "looster"))
			if err != nil {
				t.Fatal(err)
			}
			want := payload
			if tc.fail {
				want = []byte("previous binary")
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("installed binary = %q", got)
			}
			info, err := os.Stat(filepath.Join(dest, "looster"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&0111 == 0 {
				t.Fatal("binary is not executable")
			}
			if realBinary {
				installed := exec.Command(filepath.Join(dest, "looster"), "--config", "/missing/config.toml", "licenses")
				installed.Env = append(os.Environ(), "LOOSTER_DATABASE_URL=invalid")
				output, err := installed.Output()
				if err != nil {
					t.Fatal(err)
				}
				license, err := os.ReadFile("../../LICENSE")
				if err != nil {
					t.Fatal(err)
				}
				notices, err := os.ReadFile("../../THIRD_PARTY_NOTICES.md")
				if err != nil {
					t.Fatal(err)
				}
				if want := string(license) + "\n" + string(notices); string(output) != want {
					t.Fatal("installed real binary did not retain complete license notices")
				}
			}
		})
	}
}

func TestDocumentedInstallerURL(t *testing.T) {
	const canonical = "https://raw.githubusercontent.com/api-mittens/looster/master/scripts/install.sh"
	for _, path := range []string{"../../readme.md", "../../docs/installation.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "curl -fsSL "+canonical+" -o install-looster.sh") {
			t.Errorf("%s must document the canonical master installer URL", path)
		}
	}
}
