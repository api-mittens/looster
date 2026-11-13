//go:build uitest

package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// newTestBrowser returns an unstarted browser and a shutdown function that must
// be deferred before cancelling parent. A plain NewContext cancel kills Chrome's
// main process, but profile-writing children can outlive that process. Close via
// CDP first so Chrome shuts down its children before t.TempDir removes the profile.
func newTestBrowser(t *testing.T, parent context.Context, options ...chromedp.ExecAllocatorOption) (context.Context, func()) {
	t.Helper()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.ExecPath(chromiumPath(t)), chromedp.NoSandbox, chromedp.UserDataDir(t.TempDir()), chromedp.WSURLReadTimeout(60*time.Second))
	opts = append(opts, options...)
	alloc, stopAlloc := chromedp.NewExecAllocator(parent, opts...)
	tab, stopTab := chromedp.NewContext(alloc)
	return tab, func() {
		// Always reap the browser and allocator, including failed startup or a
		// failed graceful close. Do not let profile cleanup race these waits.
		defer stopAlloc()
		defer stopTab()
		if chromedp.FromContext(tab).Browser == nil {
			// Cancel and stopTab would both consume the allocation semaphore
			// if Run never reached browser allocation. Only stopTab is needed.
			return
		}
		shutdown, cancel := context.WithTimeout(tab, 10*time.Second)
		defer cancel()
		if err := chromedp.Cancel(shutdown); err != nil {
			t.Errorf("shut down Chromium: %v", err)
		}
	}
}

func TestBrowserShutdown(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to test Chromium shutdown")
	}
	// Cold Chrome startup on shared CI runners can exceed 20 seconds. Leave
	// room for the allocator's 60-second startup budget and graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Run("unstarted", func(t *testing.T) {
		_, shutdown := newTestBrowser(t, ctx)
		shutdown()
	})
	profile := t.TempDir()
	tab, shutdown := newTestBrowser(t, ctx, chromedp.UserDataDir(profile))
	// Use a scope so Fatal also runs shutdown, just like the acceptance tests.
	func() {
		defer shutdown()
		if err := chromedp.Run(tab, chromedp.Navigate("data:text/html,<title>shutdown regression</title>")); err != nil {
			t.Fatal(err)
		}
	}()
	if tab.Err() != context.Canceled {
		t.Fatalf("browser context after shutdown: %v", tab.Err())
	}
	// No sleeps or retries: profile writers must have stopped before returning.
	if err := os.RemoveAll(profile); err != nil {
		t.Fatalf("remove stopped browser profile: %v", err)
	}
}

func TestBrowserSlowStartup(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to test slow Chromium startup")
	}
	if runtime.GOOS == "windows" {
		t.Skip("startup wrapper requires a POSIX shell")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	// Delay a real browser past chromedp's default 20-second websocket timeout.
	// exec replaces the shell so cancellation still reaps the browser process.
	wrapper := filepath.Join(t.TempDir(), "slow-chrome")
	browser := "'" + strings.ReplaceAll(chromiumPath(t), "'", "'\"'\"'") + "'"
	script := "#!" + shell + "\nsleep 21\nexec " + browser + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx, chromedp.ExecPath(wrapper))
	defer shutdown()
	var title string
	if err := chromedp.Run(tab, chromedp.Navigate("data:text/html,<title>slow startup regression</title>"), chromedp.Title(&title)); err != nil {
		t.Fatal(err)
	}
	if title != "slow startup regression" {
		t.Fatalf("page title = %q", title)
	}
}
