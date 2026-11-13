//go:build uitest

package web

import (
	"github.com/dop251/goja"
	"testing"
	"time"
)

// Timers execute on the VM goroutine, never from a Go timer goroutine.
// This preserves the live simulation test rather than turning it into a static tick test.
func installJSTimers(vm *goja.Runtime) func(*testing.T, *goja.Promise) {
	type timer struct {
		callback goja.Callable
		due      time.Time
		interval time.Duration
		args     []goja.Value
	}
	timers := map[int]*timer{}
	next := 0
	schedule := func(repeat, frame bool) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			fn, ok := goja.AssertFunction(call.Argument(0))
			if !ok {
				panic(vm.NewTypeError("timer callback must be callable"))
			}
			delay := time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
			if frame {
				delay = 16 * time.Millisecond
			}
			if delay < time.Millisecond {
				delay = time.Millisecond
			}
			next++
			entry := &timer{callback: fn, due: time.Now().Add(delay)}
			if repeat {
				entry.interval = delay
			}
			if len(call.Arguments) > 2 {
				entry.args = call.Arguments[2:]
			}
			timers[next] = entry
			return vm.ToValue(next)
		}
	}
	clear := func(id int) { delete(timers, id) }
	vm.Set("setTimeout", schedule(false, false))
	vm.Set("setInterval", schedule(true, false))
	vm.Set("requestAnimationFrame", schedule(false, true))
	vm.Set("clearTimeout", clear)
	vm.Set("clearInterval", clear)
	vm.Set("cancelAnimationFrame", clear)
	return func(t *testing.T, p *goja.Promise) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for p.State() == goja.PromiseStatePending {
			if time.Now().After(deadline) {
				t.Fatal("JavaScript promise timed out")
			}
			var id int
			var earliest *timer
			for key, entry := range timers {
				if earliest == nil || entry.due.Before(earliest.due) || (entry.due.Equal(earliest.due) && key < id) {
					id = key
					earliest = entry
				}
			}
			if earliest == nil {
				t.Fatal("pending JavaScript promise has no scheduled work")
			}
			time.Sleep(time.Until(earliest.due))
			if earliest.interval == 0 {
				delete(timers, id)
			} else {
				earliest.due = time.Now().Add(earliest.interval)
			}
			if _, err := earliest.callback(goja.Undefined(), earliest.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
}
