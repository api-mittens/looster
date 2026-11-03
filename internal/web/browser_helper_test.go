package web

import (
	"context"
	"os"
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
	opts = append(opts, chromedp.ExecPath(chromiumPath(t)), chromedp.NoSandbox, chromedp.UserDataDir(t.TempDir()))
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
