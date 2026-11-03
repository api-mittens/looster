package web

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/dop251/goja"
	"github.com/jackc/pgx/v5"
)

type testLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *testLog) Write(p []byte) (int, error) { b.Lock(); defer b.Unlock(); return b.Buffer.Write(p) }
func (b *testLog) String() string              { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

// This opt-in workflow uses a disposable database and a prebuilt real CLI.
// No Node process, fixture HTTP responses, mocked SQL, or virtual browser time.
func TestGraphProduction(t *testing.T) {
	if os.Getenv("LOOSTER_PRODUCTION_TEST") != "1" {
		t.Skip("set LOOSTER_PRODUCTION_TEST=1, LOOSTER_TEST_DATABASE_URL and LOOSTER_TEST_BINARY")
	}
	db, binary := os.Getenv("LOOSTER_TEST_DATABASE_URL"), os.Getenv("LOOSTER_TEST_BINARY")
	if db == "" || binary == "" {
		t.Fatal("provide explicit disposable database URL and compiled binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ns := fmt.Sprintf("graph-e2e-%d", time.Now().UnixNano())
	other := ns + "-other"
	schemas := []string{"looster_ns_" + ns, "looster_ns_" + other}
	conn, err := pgx.Connect(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	sql := func(text string) {
		if _, err := conn.Exec(ctx, text); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		for _, schema := range schemas {
			if _, err := conn.Exec(cleanup, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
				t.Errorf("cleanup schema: %v", err)
			}
		}
	}()
	cli := func(args []string, input, namespace string) string {
		command := exec.CommandContext(ctx, binary, append([]string{"--database-url", db, "--namespace", namespace}, args...)...)
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	cli([]string{"namespace", "create", ns}, "", ns)
	cli([]string{"namespace", "create", other}, "", ns)
	cli([]string{"behaviour", "apply"}, "AGENT coder WITH \"Implementation\";\nAGENT reviewer WITH \"Review\";\nAGENT planner WITH \"Planning\";\nAGENT analyst WITH \"Analysis\";\nAGENT unrelated WITH \"Unrelated\";\nAGENT finished WITH \"Completed work only\";\nTAG src/alpha WITH \"Alpha tasks\";\nTAG src/beta WITH \"Beta tasks\";\n", ns)
	agents := []string{"coder", "reviewer", "planner", "analyst"}
	for i := 1; i <= 32; i++ {
		status := "todo"
		if i%2 == 1 {
			status = "planning"
		}
		if i == 31 {
			status = "validated"
		}
		if i == 32 {
			status = "closed"
		}
		args := []string{"task", "create", fmt.Sprintf("A deliberately long real production task title %d", i), "--status", status}
		if i == 1 || i == 3 {
			args = append(args, "--tag", "src/alpha")
		}
		if i == 2 || i == 3 {
			args = append(args, "--tag", "src/beta")
		}
		cli(args, "Created through the public CLI", ns)
		cli([]string{"task", "bind", agents[(i-1)%4], fmt.Sprintf("acceptance-session-%d", i), "--task", fmt.Sprintf("TSK-%d", i)}, "", ns)
	}
	cli([]string{"task", "bind", "finished", "completed-session", "--task", "TSK-31"}, "", ns)
	cli([]string{"task", "create", "Other namespace only"}, "Isolated task", other)
	cli([]string{"task", "comment", "TSK-1"}, "Actual comment from CLI", ns)
	for i := 0; i < 9; i++ {
		cli([]string{"task", "comment", "TSK-7"}, fmt.Sprintf("Yellow heatmap activity %d", i), ns)
	}
	for i := 0; i < 24; i++ {
		cli([]string{"task", "comment", "TSK-9"}, fmt.Sprintf("Red heatmap activity %d", i), ns)
	}
	sql(`BEGIN; SET search_path TO "` + schemas[0] + `"; UPDATE tasks SET created_at=now()-interval '365 days'; ALTER TABLE events DISABLE TRIGGER immutable_event; UPDATE events SET created_at=now()-(((task_id-1)%6)*14)*interval '1 day'; ALTER TABLE events ENABLE TRIGGER immutable_event; UPDATE sessions SET status='running' WHERE task_id=1; INSERT INTO workers(id,agents,last_seen) VALUES('acceptance-worker',ARRAY['coder','unrelated','finished'],now()+interval '5 minutes'); COMMIT;`)
	lease, err := pgx.Connect(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	if _, err := lease.Exec(ctx, `SET search_path TO "`+schemas[0]+`"; SELECT pg_advisory_lock(hashtextextended(current_schema()||':looster:session:1:coder',0));`); err != nil {
		t.Fatal(err)
	}
	server := exec.CommandContext(ctx, binary, "--database-url", db, "--namespace", ns, "web", "--listen", "127.0.0.1:0")
	log := &testLog{}
	server.Stdout = log
	server.Stderr = log
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	var base string
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		m := regexp.MustCompile(`Dashboard: (http://[^\s]+)`).FindStringSubmatch(log.String())
		if len(m) > 1 {
			base = m[1]
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if base == "" {
		t.Fatalf("server did not start: %s", log.String())
	}
	tab, stopBrowser := newTestBrowser(t, ctx, chromedp.WindowSize(1440, 1100))
	defer stopBrowser()
	var mu sync.Mutex
	requests := []map[string]interface{}{}
	chromedp.ListenTarget(tab, func(event interface{}) {
		if e, ok := event.(*network.EventRequestWillBeSent); ok {
			mu.Lock()
			requests = append(requests, map[string]interface{}{"url": e.Request.URL, "method": e.Request.Method})
			mu.Unlock()
		}
	})
	if err := chromedp.Run(tab); err != nil {
		t.Fatal(err)
	}
	var branding bool
	if err := chromedp.Run(tab,
		chromedp.Navigate(base),
		chromedp.WaitVisible("header img.logo"),
		chromedp.Evaluate(`(async () => {
const logo = document.querySelector('header img.logo');
await logo.decode();
const icon = document.querySelector('link[rel="icon"]');
const response = await fetch(icon.href);
return logo.naturalWidth > 0 && logo.alt === 'Looster' && icon.type === 'image/svg+xml' && response.ok && (await response.text()).includes('<svg');
})()`, &branding, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
	); err != nil {
		t.Fatal(err)
	}
	if !branding {
		t.Fatal("production logo or SVG favicon did not load")
	}
	vm := goja.New()
	vm.Set("base", base)
	vm.Set("other", other)
	vm.Set("schemas", schemas)
	vm.Set("getRequests", func() []map[string]interface{} {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]interface{}{}, requests...)
	})
	vm.Set("releaseLease", func() {
		if err := lease.Close(ctx); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	vm.Set("cli", func(args []string, input string) string { return cli(args, input, ns) })
	vm.Set("sql", sql)
	vm.Set("delay", func(ms int) {
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
		case <-ctx.Done():
			panic(vm.NewGoError(ctx.Err()))
		}
	})
	installCDPBridge(vm, tab)
	body := strings.ReplaceAll(graphproductionProbe, "requests.", "getRequests().")
	script := `const checks=[];const check=(ok,label)=>{if(!ok)throw Error(label);checks.push(label);};async function until(fn){for(let i=0;i<200;i++){try{if(await fn())return;}catch{}await delay(25);}throw Error('Timed out '+fn);} (async()=>{` + body + `;return checks;})()`
	result, err := vm.RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := result.Export().(*goja.Promise)
	if !ok || p.State() != goja.PromiseStateFulfilled {
		if ok {
			t.Fatalf("production checks: %v", p.Result())
		}
		t.Fatal("production probe did not return a promise")
	}
	t.Log(p.Result())
}
