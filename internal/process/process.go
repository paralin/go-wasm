package process

import (
	"fmt"
	"strings"
	"syscall/js"

	"github.com/johnstarich/go-wasm/internal/interop"
	"github.com/johnstarich/go-wasm/internal/js/fs"
	"github.com/johnstarich/go-wasm/internal/promise"
	"github.com/johnstarich/go-wasm/log"
	"go.uber.org/atomic"
)

const (
	minPID = 1
)

var (
	jsGo       = js.Global().Get("Go")
	jsWasm     = js.Global().Get("WebAssembly")
	uint8Array = js.Global().Get("Uint8Array")
)

var (
	pids    = make(map[PID]*process)
	lastPID = atomic.NewUint64(minPID)
)

type Process interface {
	PID() PID
	ParentPID() PID

	Start() error
	Wait() error
}

type process struct {
	pid, ppid PID
	command   string
	args      []string
	state     string

	err  error
	done chan struct{}
}

func New(command string, args []string) Process {
	return &process{
		pid:     PID(lastPID.Inc()),
		command: command,
		args:    args,
		done:    make(chan struct{}),
	}
}

func (p *process) PID() PID {
	return p.pid
}

func (p *process) ParentPID() PID {
	return p.ppid
}

func (p *process) Start() error {
	return p.startWasm()
}

func (p *process) Wait() error {
	<-p.done
	return p.err
}

func (p *process) startWasm() error {
	pids[p.pid] = p
	p.state = "pending"
	log.Printf("Spawning process [%d] %q: %s", p.pid, p.command, strings.Join(p.args, " "))
	buf, err := fs.ReadFile(p.command)
	if err != nil {
		return err
	}
	go p.runWasmBytes(buf)
	return nil
}

func (p *process) runWasmBytes(wasm []byte) {
	handleErr := func(err error) {
		p.state = "done"
		if err != nil {
			log.Errorf("Failed to start process: %s", err.Error())
			p.err = err
			p.state = "error"
		}
		close(p.done)
	}

	p.state = "compiling wasm"
	goInstance := jsGo.New()
	goInstance.Set("argv", interop.SliceFromStrings(p.args))
	importObject := goInstance.Get("importObject")
	jsBuf := uint8Array.New(len(wasm))
	js.CopyBytesToJS(jsBuf, wasm)
	// TODO add module caching
	instantiatePromise := promise.New(jsWasm.Call("instantiate", jsBuf, importObject))
	module, err := promise.Await(instantiatePromise)
	if err != nil {
		handleErr(err)
		return
	}

	instance := module.Get("instance")
	exports := instance.Get("exports")

	runFn := exports.Get("run")
	resumeFn := exports.Get("resume")
	wrapperExports := map[string]interface{}{
		"run": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			prev := switchContext(p.pid)
			ret := runFn.Invoke(interop.SliceFromJSValues(args)...)
			switchContext(prev)
			return ret
		}),
		"resume": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			prev := switchContext(p.pid)
			ret := resumeFn.Invoke(interop.SliceFromJSValues(args)...)
			switchContext(prev)
			return ret
		}),
	}
	for export, value := range interop.Entries(exports) {
		_, overridden := wrapperExports[export]
		if !overridden {
			wrapperExports[export] = value
		}
	}
	instance = js.ValueOf(map[string]interface{}{ // Instance.exports is read-only, so create a shim
		"exports": wrapperExports,
	})

	p.state = "running"
	runPromise := promise.New(goInstance.Call("run", instance))
	_, err = promise.Await(runPromise)
	handleErr(err)
}

func (p *process) JSValue() js.Value {
	return js.ValueOf(map[string]interface{}{
		"pid":  p.pid,
		"ppid": p.ppid,
	})
}

func (p *process) String() string {
	return fmt.Sprintf("PID=%s, State=%s, Err=%+v", p.pid, p.state, p.err)
}

func Dump() interface{} {
	return fmt.Sprintf("%v", pids)
}
