//go:build windows

// Shared GUI helpers for managing the four lifecycle-backed runners
// (clicker, AutoPot, KeyChain, TimerKey) and the "press a key to bind
// to a slot" interaction. Replaces ~120 LOC of duplicated logic across
// main.go, autopot_tab.go, timer_key_tab.go, keychain_tab.go, and
// clicker_tab.go.
package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"sync"

	"ezrokit/runner"
)

// lifecycleRunner is the minimum surface needed to drive a backed
// runner: Start, Stop, Wait. Every GUI runner — Runner,
// AutoPotRunner, KeyChainRunner, TimerKeyRunner — satisfies it.
type lifecycleRunner interface {
	Start() error
	Stop()
	Wait()
}

// nilable reports whether v is the nil value of its dynamic type.
//
// Used by startLifecycle to safely check whether take() returned an
// empty slot, without tripping the Go-generics restriction that
// forbids comparing a type parameter to untyped nil.
func nilable(v any) bool {
	// reflect check catches typed nil (e.g. (*fakeRunner)(nil) boxed into
	// any), which the plain `v == nil` comparison misses because the
	// interface carries a concrete type. A typed nil is still nil in
	// every meaningful sense for lifecycle tear-down: Stop/Wait must not
	// be called on it.
	if v == nil {
		return true
	}
	return reflect.ValueOf(v).IsNil()
}

// makeLifecycleSlot builds a (take, store) pair that operate on a
// typed pointer-to-pointer slot. The take closure reads the field
// under mu and nils it (via the typed zero of R); the store closure
// writes a new value under mu.
//
// The take/store signatures use the concrete R (not the
// lifecycleRunner interface) so the *ref = r assignment in store has
// no type-assertion risk and the construct closure can return the
// concrete type.
//
// Callers MUST supply the explicit type parameter — Go's inference
// from a `**T` argument paired with the interface constraint is
// unreliable here, and that's exactly why the prior `**R`-slot
// attempt failed. Example:
//
//	take, store := makeLifecycleSlot[*runner.AutoPotRunner](
//	    &a.mu, &a.autopotRunner,
//	)
//
// R must be a pointer type (e.g. *runner.AutoPotRunner) so the zero
// value is a true nil — the take closure relies on this to clear
// the slot. A runtime guard below panics at the call site with a
// clear message if a future caller passes a non-pointer R; the
// pointer-kind check is loose (any pointer type passes) because
// the *ref = r / *ref = zero assignments work for any pointer R.
func makeLifecycleSlot[R lifecycleRunner](
	mu *sync.Mutex,
	ref *R,
) (take func() R, store func(R)) {
	// Runtime guard for the R-pointer requirement. Fires
	// immediately so misuse is caught at the call site (the panic
	// stack points at the bad generic arg, not deep in a take/store
	// call). The reflect check inspects the kind of the *R
	// pointed-to value: a pointer type has Kind == Ptr, a struct
	// value has Kind == Struct, etc. Non-pointer R would silently
	// get *ref = zero (a zero struct) on take instead of a clear.
	if k := reflect.ValueOf(ref).Elem().Kind(); k != reflect.Ptr {
		panic(fmt.Sprintf(
			"makeLifecycleSlot: R must be a pointer type; got a non-pointer type whose kind is %s",
			k,
		))
	}
	var zero R
	take = func() R {
		mu.Lock()
		defer mu.Unlock()
		r := *ref
		*ref = zero
		return r
	}
	store = func(r R) {
		mu.Lock()
		defer mu.Unlock()
		*ref = r
	}
	return take, store
}

// startLifecycle starts (or replaces) a lifecycle-backed runner.
//
// This helper is intentionally separate from the runner-internal
// `internal/lifecycle.Lifecycle[C]` (which governs the goroutine
// bookkeeping inside each runner). The GUI layer's concern is the
// slot-field swap-and-restart: tear down the old runner held in a
// struct field, build a new one with the current session wired in,
// then publish the new pointer. That is a *different* layer of
// orchestration, so it has its own helper. The runners DO delegate
// to `internal/lifecycle.Lifecycle` internally, so that type is
// used transitively through each runner's Start/Stop/Wait.
//
// Generic on R so the slot accessors and the runner-construction
// closure share the same concrete type. R is inferred from the
// take+store closures (which in turn come from
// makeLifecycleSlot[*runner.X]). Because Go generics forbid
// comparing a type parameter to untyped nil, the nil check is
// routed through nilable() above.
//
// Pipeline:
//  1. isWanted gates the call; a `false` is a silent no-op (so
//     sync*Settings can call us freely even when nothing is bound).
//  2. session() snapshots the current InputSession under mu. A nil
//     session is treated as "not ready yet" — caller can retry later.
//  3. The old runner is fetched via take() and Stop()+Wait()'d if
//     non-empty.
//  4. construct() builds the new instance with sess + log wired.
//  5. Start() runs; failures are logged with `label`. NOTE: on
//     failure, the slot remains nil (the old runner was already torn
//     down by step 3). This is intentional fail-safe behavior: a
//     failed restart should not leave a half-running runner in the
//     slot. sync*Settings callers that observe nil on next
//     invocation will simply start a fresh one.
//  6. On success the new runner is written to the slot via store()
//     and "<label> started" is logged.
//
// Returns true if the new runner is now live on the slot.
func startLifecycle[R lifecycleRunner](
	take func() R,
	store func(R),
	label string,
	log func(string),
	session func() runner.InputSession,
	isWanted func() bool,
	construct func(sess runner.InputSession) R,
) bool {
	if !isWanted() {
		return false
	}
	sess := session()
	if sess == nil {
		return false
	}
	old := take()
	if !nilable(old) {
		old.Stop()
		old.Wait()
	}
	r := construct(sess)
	if err := r.Start(); err != nil {
		log(fmt.Sprintf("%s start failed: %v", label, err))
		return false
	}
	log(fmt.Sprintf("%s started", label))
	store(r)
	return true
}

// bindKeyFlow runs the "user presses a key to bind" interaction that
// appears in four places: bindClickerKey, bindAutoPotKey, bindTimerKey,
// bindKeyChainKey.
//
// Pipeline:
//  1. gate is checked synchronously. Side-effects (e.g. setting a
//     binding-slot index so concurrent binds are rejected) are OK
//     inside gate — they happen before the goroutine spawns. `false`
//     bails without spawning.
//  2. prompt is logged once.
//  3. A goroutine waits for a keypress with runner.WaitForKeyPress
//     (runner.KeyBindTimeout).
//  4. The result is dispatched back to the UI thread via
//     mainWindow.Synchronize, where:
//     * timeout → "Key bind timed out"
//     * unsupported VK (VKToHID fails) → "Key <name> is not supported"
//     * otherwise onPress(vk) runs.
//  5. cleanup runs (off-UI) once the goroutine exits, regardless of
//     outcome — use it to un-register binding state.
//  6. reenable runs on the UI thread after cleanup, refreshing bind
//     button enabled state.
//
// Returns true if a binding goroutine was spawned.
func (a *guiApp) bindKeyFlow(
	gate func() bool,
	prompt string,
	cleanup func(),
	reenable func(),
	onPress func(vk int32),
) bool {
	if !gate() {
		return false
	}
	a.appendLog(prompt)
	ctx := a.lifetimeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	var cleanupOnce sync.Once
	runCleanup := func() { cleanupOnce.Do(cleanup) }
	go func() {
		defer func() {
			if r := recover(); r != nil {
				_, _ = fmt.Fprintf(os.Stderr, "PANIC in bindKeyFlow: %v\n%s\n", r, debug.Stack())
				runCleanup()
			}
		}()
		defer func() {
			runCleanup()
			if ctx.Err() != nil {
				return
			}
			a.mainWindow.Synchronize(func() {
				reenable()
			})
		}()
		vk, ok := runner.WaitForKeyPressContext(ctx, runner.KeyBindTimeout)
		if ctx.Err() != nil {
			return
		}
		a.mainWindow.Synchronize(func() {
			if !ok {
				a.appendLog("Key bind timed out")
				return
			}
			if _, hidOK := runner.VKToHID(vk); !hidOK {
				a.appendLog(fmt.Sprintf("Key %s is not supported", runner.KeyName(vk)))
				return
			}
			onPress(vk)
		})
	}()
	return true
}
