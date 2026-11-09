package web

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/task"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func TestPerformanceBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	now := time.Now().UTC().Add(-time.Minute)
	repo := &performanceTestRepo{tasks: []task.Task{{ID: 1, Status: task.Validated, LastActivityAt: now}, {ID: 2, Status: task.Closed, LastActivityAt: now}}}
	server := httptest.NewServer(Handler(repo))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	check := func(expression, message string) {
		t.Helper()
		var valid bool
		if err := chromedp.Run(tab, chromedp.Evaluate(expression, &valid)); err != nil {
			t.Fatal(err)
		}
		if !valid {
			t.Fatal(message)
		}
	}
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible(`nav a[href="#performance"]`), chromedp.Evaluate(`document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'g',bubbles:true}));document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'p',bubbles:true}));`, nil), chromedp.WaitVisible(`.performance-chart`)); err != nil {
		t.Fatal(err)
	}
	check(`location.hash==='#performance' && document.querySelector('h1').textContent==='Performance' && document.querySelector('nav a[aria-current="page"]').hash==='#performance' && document.querySelector('#board-columns').hidden`, "gp must navigate to the performance tab")
	check(`document.querySelectorAll('.performance-chart svg').length===7 && document.querySelectorAll('.performance-counters strong')[0].textContent==='100.0%' && document.querySelectorAll('.performance-counters strong')[1].textContent==='1' && document.querySelector('[aria-label="Autonomous agents"]') && document.querySelectorAll('.performance-data tbody tr').length===30`, "real endpoint must render counters, charts, daily data and taskless subsection")
	if err := chromedp.Run(tab, emulation.SetDeviceMetricsOverride(390, 844, 1, true)); err != nil {
		t.Fatal(err)
	}
	check(`document.documentElement.scrollWidth<=window.innerWidth && [...document.querySelectorAll('.performance-chart svg')].every(svg=>svg.getBoundingClientRect().width>0 && svg.getBoundingClientRect().right<=window.innerWidth)`, "mobile performance charts must fit the viewport")
	if err := chromedp.Run(tab, chromedp.Click(`#performance-refresh`), chromedp.WaitVisible(`.performance-chart`), chromedp.Navigate(server.URL+"/#performance"), chromedp.WaitVisible(`.performance-chart`)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelectorAll('.performance-chart').length===7`, "direct route must load the performance API")
}
