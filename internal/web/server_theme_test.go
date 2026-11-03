package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// Exercise the shipped renderer and embedded CSS in Chromium, not a copied
// settings markup or a CSS string matcher. No database or Node is required.
func TestServerSettingsThemeBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to run Chromium theme regression")
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/theme-test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><head><link rel="stylesheet" href="/style.css"></head><body><main id="page"></main><script type="module">
import {serversHTML} from '/views.mjs';
document.querySelector('#page').innerHTML = serversHTML({workers:[{id:'theme-worker',last_seen:'2026-11-03T16:00:00Z',agents:['coder','reviewer','analyst','agent-with-a-very-long-unbroken-name-for-mobile-layout'],settings:{harness:'jcode',concurrency:4,poll_interval:'5s',run_timeout:'30m'}}],agents:[{name:'uncovered'},{name:'other'}],sessions:[{agent:'coder',status:'running'},{agent:'coder',status:'running'}]});
</script></body></html>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	for _, tc := range []struct{ mode, background, label, value, border string }{
		{"dark", "rgb(23, 27, 34)", "rgb(170, 178, 192)", "rgb(228, 228, 231)", "rgb(59, 65, 77)"},
		{"light", "rgb(250, 250, 250)", "rgb(113, 113, 122)", "rgb(24, 24, 27)", "rgb(228, 228, 231)"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var got struct {
				Background, Border                       string
				Labels, Values, LabelColors, ValueColors []string
				Dark                                     bool
			}
			err := chromedp.Run(tab,
				emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: tc.mode}}),
				chromedp.Navigate(server.URL+"/theme-test"),
				chromedp.WaitVisible(".server-settings dd"),
				chromedp.Evaluate(`(() => {
const settings = document.querySelector('.server-settings');
const labels = [...settings.querySelectorAll('dt')], values = [...settings.querySelectorAll('dd')];
return {Background:getComputedStyle(settings).backgroundColor, Border:getComputedStyle(settings).borderTopColor,
Labels:labels.map(e=>e.textContent), Values:values.map(e=>e.textContent),
LabelColors:labels.map(e=>getComputedStyle(e).color), ValueColors:values.map(e=>getComputedStyle(e).color),
Dark:matchMedia('(prefers-color-scheme: dark)').matches};
})()`, &got),
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Dark != (tc.mode == "dark") {
				t.Fatalf("media emulation failed: %+v", got)
			}
			if !reflect.DeepEqual(got.Labels, []string{"Harness", "Concurrency", "Poll interval", "Run timeout"}) || !reflect.DeepEqual(got.Values, []string{"jcode", "4", "5s", "30m"}) {
				t.Fatalf("shipped settings not rendered: %+v", got)
			}
			if got.Background != tc.background || got.Border != tc.border {
				t.Errorf("settings surface: background=%s border=%s, want %s / %s", got.Background, got.Border, tc.background, tc.border)
			}
			for i := range got.Labels {
				if got.LabelColors[i] != tc.label || got.ValueColors[i] != tc.value {
					t.Errorf("%s foreground: %s / %s, want %s / %s", got.Labels[i], got.LabelColors[i], got.ValueColors[i], tc.label, tc.value)
				}
			}
			for _, width := range []int64{1280, 375} {
				var layout struct {
					Horizontal, Wrapped, Contained, Matching, Badges bool
				}
				err := chromedp.Run(tab,
					emulation.SetDeviceMetricsOverride(width, 900, 1, false),
					chromedp.Evaluate(`(() => {
const rail = document.querySelector('.server-agents');
const items = [...rail.children], rects = items.map(e => e.getBoundingClientRect());
const bounds = rail.getBoundingClientRect();
const orphan = document.querySelector('.orphaned-coverage');
const style = getComputedStyle(rail), other = getComputedStyle(orphan);
return {
Horizontal: Math.abs(rects[0].top - rects[1].top) < 1 && rects[1].left > rects[0].right,
Wrapped: rects.some(r => r.top > rects[0].top),
Contained: rects.every(r => r.left >= bounds.left && r.right <= bounds.right + 1) && rail.scrollWidth <= rail.clientWidth && document.documentElement.scrollWidth <= innerWidth,
Matching: style.display === 'flex' && style.flexDirection === 'row' && style.flexWrap === 'wrap' && style.gap === other.gap && other.display === 'flex',
Badges: items[0].querySelector('.badge.running')?.textContent === '2 running' && items.slice(1).every(e => e.querySelector('.badge')?.textContent === 'Idle') && items.every(e => { const b = e.querySelector('button').getBoundingClientRect(), s = e.querySelector('.badge').getBoundingClientRect(); return s.left >= b.right && s.top >= e.getBoundingClientRect().top && s.bottom <= e.getBoundingClientRect().bottom; })
};
})()`, &layout),
				)
				if err != nil {
					t.Fatal(err)
				}
				if !layout.Contained || !layout.Matching || !layout.Badges || (width == 1280 && !layout.Horizontal) || (width == 375 && !layout.Wrapped) {
					t.Errorf("%s %dpx rail layout: %+v", tc.mode, width, layout)
				}
				t.Logf("%s %dpx rail layout: %+v", tc.mode, width, layout)
			}
			t.Logf("%s settings: background=%s border=%s labels=%v values=%v", tc.mode, got.Background, got.Border, got.LabelColors, got.ValueColors)
		})
	}
}
