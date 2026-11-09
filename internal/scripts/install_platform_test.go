package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerRejectsRemovedWindowsTargetsBeforeDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	for _, host := range []string{"MINGW64_NT-10.0", "MSYS_NT-10.0", "CYGWIN_NT-10.0"} {
		t.Run(host, func(t *testing.T) {
			dir := t.TempDir()
			tools := filepath.Join(dir, "tools")
			if err := os.Mkdir(tools, 0755); err != nil {
				t.Fatal(err)
			}
			for name, script := range map[string]string{
				"uname": "#!/bin/sh\nprintf '%s\\n' '" + host + "'\n",
				"curl":  "#!/bin/sh\nprintf called > \"$REQUEST_LOG\"\nexit 77\n",
			} {
				if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0755); err != nil {
					t.Fatal(err)
				}
			}
			requestLog := filepath.Join(dir, "requests")
			cmd := exec.Command("sh", "../../scripts/install.sh")
			cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "LOOSTER_REPO=api-mittens/looster", "REQUEST_LOG="+requestLog)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "Windows binaries are no longer published") {
				t.Fatalf("unsupported host: err=%v output=%s", err, out)
			}
			if _, err := os.Stat(requestLog); !os.IsNotExist(err) {
				t.Fatalf("unsupported host attempted a download: %v", err)
			}
		})
	}
}
