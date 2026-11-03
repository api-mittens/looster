package web

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// These are behavioral tests of the shipped JS, not Go reimplementations of it.
// Goja supplies the ECMAScript runtime, and the small host below supplies only the
// browser primitives used by the existing isolated custom-element tests.
func TestDashboardModules(t *testing.T) {
	for _, suite := range []struct{ name, source string }{{"search", searchProbe}, {"output", outputProbe}, {"namespace", namespaceProbe}, {"graph", graphProbe}} {
		t.Run(suite.name, func(t *testing.T) { runJSProbe(t, suite.source) })
	}
}

var importPattern = regexp.MustCompile(`(?m)^import (.+?) from ['"](.+?)['"];?`)
var exportPattern = regexp.MustCompile(`export (?:function|class|const) ([A-Za-z_$][\w$]*)`)

func transformJS(source string) string {
	source = importPattern.ReplaceAllStringFunc(source, func(s string) string {
		m := importPattern.FindStringSubmatch(s)
		binding := m[1]
		if strings.HasPrefix(binding, "* as ") {
			binding = strings.TrimPrefix(binding, "* as ")
		}
		return "const " + binding + " = require(" + fmt.Sprintf("%q", m[2]) + ");"
	})
	names := exportPattern.FindAllStringSubmatch(source, -1)
	source = strings.ReplaceAll(source, "export ", "")
	for _, m := range names {
		source += "\nexports." + m[1] + " = " + m[1] + ";"
	}
	return source
}
func runJSProbe(t *testing.T, source string) {
	t.Helper()
	vm := goja.New()
	pump := installJSTimers(vm)
	// The interpreter is substantially slower under Go's race instrumentation.
	// Bound initialization and each individual probe, not cumulative suite time.
	timer := time.AfterFunc(3*time.Minute, func() { vm.Interrupt("JavaScript probe exceeded 3 minutes") })
	defer timer.Stop()
	vm.Set("readFileSync", func(name string) string {
		b, err := os.ReadFile(filepath.Clean(strings.TrimLeft(strings.TrimPrefix(name, "file:"), "/")))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return string(b)
	})
	vm.Set("readFile", func(name string) string {
		b, err := os.ReadFile(filepath.Clean(strings.TrimLeft(strings.TrimPrefix(name, "file:"), "/")))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return string(b)
	})
	vm.Set("resolveURL", func(path, base string) map[string]interface{} {
		u, err := url.Parse(base)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		p, err := url.Parse(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		u = u.ResolveReference(p)
		q := map[string]interface{}{}
		for k, v := range u.Query() {
			q[k] = v[0]
		}
		return map[string]interface{}{"href": u.String(), "query": q}
	})
	cache := map[string]goja.Value{}
	var load func(string, string) goja.Value
	load = func(name, dir string) goja.Value {
		path := filepath.Clean(filepath.Join(dir, name))
		if value, ok := cache[path]; ok {
			return value
		}
		if strings.HasSuffix(path, "d3-force-adapter.mjs") {
			return load("./vendor/d3.v7.9.0.min.js", "static")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		object := vm.NewObject()
		cache[path] = object
		fn, err := vm.RunScript(path, "(function(require,exports,module){\n"+transformJS(string(b))+"\n})")
		if err != nil {
			panic(vm.NewGoError(err))
		}
		callable, _ := goja.AssertFunction(fn)
		module := vm.NewObject()
		module.Set("exports", object)
		_, err = callable(goja.Undefined(), vm.ToValue(func(n string) goja.Value { return load(n, filepath.Dir(path)) }), object, module)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		cache[path] = module.Get("exports")
		return cache[path]
	}
	vm.Set("require", func(name string) goja.Value { return load(name, ".") })
	if _, err := vm.RunString(jsTestHost); err != nil {
		t.Fatal(err)
	}
	source = regexp.MustCompile(`(?m)^import .*\n`).ReplaceAllStringFunc(source, func(s string) string {
		m := importPattern.FindStringSubmatch(strings.TrimSpace(s))
		if len(m) == 0 {
			panic("invalid module import in Go-owned probe")
		}
		return "const " + m[1] + " = require(" + fmt.Sprintf("%q", m[2]) + ");\n"
	})
	result, err := vm.RunString("(async()=>{\n" + source + "\nreturn tests;})()")
	if err != nil {
		t.Fatal(err)
	}
	promise, ok := result.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled {
		if ok {
			t.Fatalf("suite initialization: %v", promise.Result())
		}
		t.Fatal("suite did not complete")
	}
	tests := promise.Result().ToObject(vm)
	for i := 0; i < int(tests.Get("length").ToInteger()); i++ {
		entry := tests.Get(fmt.Sprint(i)).ToObject(vm)
		t.Run(entry.Get("name").String(), func(t *testing.T) {
			timer.Reset(3 * time.Minute)
			defer timer.Stop()
			fn, _ := goja.AssertFunction(entry.Get("fn"))
			context := vm.NewObject()
			context.Set("after", func(call goja.FunctionCall) goja.Value {
				cleanup, ok := goja.AssertFunction(call.Argument(0))
				if !ok {
					panic(vm.NewTypeError("cleanup must be a function"))
				}
				t.Cleanup(func() {
					if _, err := cleanup(goja.Undefined()); err != nil {
						t.Error(err)
					}
				})
				return goja.Undefined()
			})
			value, err := fn(goja.Undefined(), context)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := value.Export().(*goja.Promise); ok {
				pump(t, p)
				if p.State() != goja.PromiseStateFulfilled {
					t.Fatalf("async assertion did not fulfill: %v", p.Result())
				}
			}
		})
	}
}

const jsTestHost = `
const tests=[];
const test=(name,fn)=>tests.push({name,fn});
const fail=(message)=>{throw Error(message||'assertion failed');};
function same(a,b){if(Object.is(a,b))return true;if(!a||!b||typeof a!=='object'||typeof b!=='object')return false;if(a instanceof Map){if(!(b instanceof Map)||a.size!==b.size)return false;return [...a].every(([k,v])=>b.has(k)&&same(v,b.get(k)));}if(a instanceof Set){if(!(b instanceof Set)||a.size!==b.size)return false;return [...a].every(v=>b.has(v));}const ak=Object.keys(a),bk=Object.keys(b);return ak.length===bk.length&&ak.every(k=>Object.prototype.hasOwnProperty.call(b,k)&&same(a[k],b[k]));}
const assert={ok:(a,m)=>{if(!a)fail(m);},equal:(a,b,m)=>{if(a!==b)fail(m||String(a)+' !== '+String(b));},notEqual:(a,b,m)=>{if(a===b)fail(m);},notDeepEqual:(a,b,m)=>{if(same(a,b))fail(m||'unexpected deep equality');},deepEqual:(a,b,m)=>{if(!same(a,b))fail(m||JSON.stringify(a)+' != '+JSON.stringify(b));},match:(a,b,m)=>{if(!b.test(a))fail(m||String(a)+' does not match '+b);},doesNotMatch:(a,b,m)=>{if(b.test(a))fail(m||'unexpected match '+b);},rejects:async(p,want)=>{let caught;try{await p;}catch(e){caught=e;}if(!caught||!Object.keys(want).every(k=>caught[k]===want[k]))fail('unexpected rejection '+caught);}};
class Event {constructor(type,options={}){this.type=type;Object.assign(this,options);this.defaultPrevented=false;}preventDefault(){if(this.cancelable)this.defaultPrevented=true;}}
class EventTarget {constructor(){this.handlers=new Map();}addEventListener(t,fn){if(!this.handlers.has(t))this.handlers.set(t,new Set());this.handlers.get(t).add(fn);}removeEventListener(t,fn){this.handlers.get(t)?.delete(fn);}dispatchEvent(e){for(const fn of this.handlers.get(e.type)||[])typeof fn==='function'?fn.call(this,e):fn.handleEvent(e);return !e.defaultPrevented;}}
class DOMException extends Error {constructor(message,name){super(message);this.name=name;}}
class AbortController {constructor(){this.signal={aborted:false};}abort(){this.signal.aborted=true;}}
class URL {constructor(path,base){const r=resolveURL(String(path),base||'file:./probe');this.href=r.href;this.values=r.query;this.searchParams={get:k=>this.values[k]??null,set:(k,v)=>this.values[k]=String(v)};}toString(){return this.href;}}
const queueMicrotask=fn=>Promise.resolve().then(fn);
const performance={now:()=>Date.now()};
const vm={createContext:context=>context,runInContext:(source,context)=>Function(...Object.keys(context),source)(...Object.values(context))};
`
