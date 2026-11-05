package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/behaviour"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

type inspectorBrowserRepo struct{ testRepo }

func (*inspectorBrowserRepo) Agents(context.Context) ([]agent.Agent, error) {
	return []agent.Agent{{Name: "solo", Description: "Standalone prompt", TasklessOnly: true}}, nil
}
func (*inspectorBrowserRepo) Sessions(context.Context, string) ([]agent.Session, error) {
	return []agent.Session{{JobID: 100, Agent: "solo", Status: "running", SessionID: "live-session"}}, nil
}
func (*inspectorBrowserRepo) Behaviour(context.Context) ([]behaviour.Rule, error) {
	return []behaviour.Rule{{Trigger: "every", Target: "solo", IntervalSeconds: 60}}, nil
}
func (*inspectorBrowserRepo) SessionHistory(_ context.Context, name string, _ int64, before int64, limit int) ([]agent.SessionRun, error) {
	out := []agent.SessionRun{}
	for id := int64(23); id >= 1; id-- {
		if name == "solo" && (before == 0 || id < before) {
			out = append(out, browserRun(id))
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (*inspectorBrowserRepo) SessionRun(_ context.Context, id int64) (agent.SessionRun, error) {
	if id < 1 || id > 23 {
		return agent.SessionRun{}, app.ErrNotFound
	}
	return browserRun(id), nil
}
func browserRun(id int64) agent.SessionRun {
	return agent.SessionRun{JobID: id, Agent: "solo", SessionID: fmt.Sprintf("session-%d", id), Outcome: "done", Summary: fmt.Sprintf("Summary %d <safe>", id), SummaryStatus: "available", StartedAt: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 11, 4, 10, 1, 0, 0, time.UTC)}
}

// KeyEvent emits an explicit char event after keydown even when keydown called
// preventDefault. When SVG keydown opens a modal, that char would activate the
// newly focused Close button. Hardware-style events respect the handled keydown.
func rawInspectorKey(key, code string, virtual int64) chromedp.Tasks {
	return chromedp.Tasks{
		input.DispatchKeyEvent(input.KeyDown).WithKey(key).WithCode(code).WithWindowsVirtualKeyCode(virtual),
		input.DispatchKeyEvent(input.KeyUp).WithKey(key).WithCode(code).WithWindowsVirtualKeyCode(virtual),
	}
}

func TestAgentInspectorBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to run Chromium acceptance")
	}
	server := httptest.NewServer(Handler(&inspectorBrowserRepo{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	checkpoint := func(label string) chromedp.Action {
		return chromedp.ActionFunc(func(context.Context) error { t.Log(label); return nil })
	}
	if err := chromedp.Run(tab,
		emulation.SetFocusEmulationEnabled(true),
		chromedp.Navigate(server.URL+"/#board"),
		chromedp.WaitVisible(`task-card [data-session-agent="solo"]`),
		chromedp.Focus(`task-card [data-session-agent="solo"]`), chromedp.KeyEvent(kb.Enter),
		chromedp.WaitVisible(`#selected-run .run-summary`),
		chromedp.Poll(`document.querySelector('#selected-run').textContent.includes('pending') && document.querySelector('#selected-run').textContent.includes('live-session')`, nil),
		chromedp.Click(`[data-run="23"]`),
		chromedp.Poll(`document.querySelector('#selected-run').textContent.includes('Summary 23 <safe>')`, nil),
		chromedp.Click(`[data-run-before]`), chromedp.WaitVisible(`[data-run="3"]`),
		chromedp.Click(`[data-run-previous]`), chromedp.WaitVisible(`[data-run="23"]`),
		chromedp.Click(`#close`),
		chromedp.Poll(`!document.querySelector('dialog').open`, nil),
		chromedp.Evaluate(`location.hash='graph'`, nil), chromedp.WaitVisible(`task-agent-graph [data-key="agent:solo"]`),
		chromedp.Focus(`task-agent-graph [data-key="agent:solo"]`), rawInspectorKey("Enter", "Enter", 13),
		checkpoint("SVG keyboard activated"),
		chromedp.WaitVisible(`#selected-run .run-summary`),
		chromedp.Poll(`document.querySelector('dialog').open && document.querySelector('#dialog-title').textContent==='@ solo' && document.querySelector('#selected-run').textContent.includes('Summary 23 <safe>')`, nil),
		chromedp.Click(`#close`),
		chromedp.Poll(`!document.querySelector('dialog').open`, nil),
		chromedp.Evaluate(`location.hash='system'`, nil), chromedp.WaitVisible(`workflow-graph [data-agent="solo"]`),
		chromedp.Focus(`workflow-graph [data-agent="solo"]`), rawInspectorKey(" ", "Space", 32),
		checkpoint("SVG keyboard activated"),
		chromedp.WaitVisible(`#selected-run .run-summary`),
		chromedp.Poll(`document.querySelector('dialog').open && document.querySelector('#dialog-title').textContent==='@ solo' && document.querySelector('#selected-run').textContent.includes('Summary 23 <safe>')`, nil),
		checkpoint("testing close/reopen race"),
		// A queued close event from the previous opening must not clear the new inspector.
		chromedp.Evaluate(`{const board=document.querySelector('looster-board');board.dialog.addEventListener('close',()=>{document.body.dataset.inspectorReopened=String(board.dialog.open && board.agentInspection?.name==='solo');},{once:true});board.dialog.close();board.openAgent('solo');}`, nil),
		chromedp.Poll(`document.body.dataset.inspectorReopened==='true' && document.querySelector('dialog').open && document.querySelector('#selected-run')?.textContent.includes('Summary 23 <safe>')`, nil, chromedp.WithPollingInterval(50*time.Millisecond)),
	); err != nil {
		var state string
		_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({open:document.querySelector('dialog').open,inspection:document.querySelector('looster-board').agentInspection?.name,history:document.querySelector('#agent-history')?.textContent,selected:document.querySelector('#selected-run')?.textContent})`, &state))
		t.Fatalf("%v: %s", err, state)
	}
}
