//go:build uitest

package web

import (
	"context"
	"encoding/json"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/dop251/goja"
	"os"
	"testing"
	"time"
)

func installCDPBridge(vm *goja.Runtime, tab context.Context) {
	vm.Set("call", func(method string, params map[string]interface{}) {
		if params == nil {
			params = map[string]interface{}{}
		}
		raw, err := json.Marshal(params)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		err = chromedp.Run(tab, chromedp.ActionFunc(func(ctx context.Context) error { return cdp.Execute(ctx, method, json.RawMessage(raw), nil) }))
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	vm.Set("evaluate", func(expression string) interface{} {
		var value interface{}
		err := chromedp.Run(tab, chromedp.Evaluate(expression, &value, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return value
	})
}

func TestProductionProbeSyntax(t *testing.T) {
	if _, err := goja.Compile("production probe", "(async()=>{\n"+graphproductionProbe+"\n})", false); err != nil {
		t.Fatal(err)
	}
}

// Check the production transport without a database, under the browser opt-in.
func TestGraphCDPBridge(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to test Chromium CDP bridge")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tab, stopBrowser := newTestBrowser(t, ctx)
	defer stopBrowser()
	if err := chromedp.Run(tab); err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	installCDPBridge(vm, tab)
	value, err := vm.RunString(`(async()=>{await call('Network.enable');await call('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:false});await call('Input.dispatchKeyEvent',{type:'keyDown',key:'g'});const result=await evaluate('Promise.resolve({answer:42,width:innerWidth})');if(result.answer!==42||result.width!==390)throw Error('CDP result mismatch');return true;})()`)
	if err != nil {
		t.Fatal(err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled || !promise.Result().ToBoolean() {
		if ok {
			t.Fatalf("CDP bridge: %v", promise.Result())
		}
		t.Fatal("CDP probe did not fulfill")
	}
}
