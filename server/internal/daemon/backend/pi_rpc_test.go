package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// testRPCRequest is the JSON structure the daemon sends to pi's stdin
// when starting an RPC session. The test fake decodes this to verify
// the daemon composed the prompt correctly.
type testRPCRequest struct {
	Type         string   `json:"type"`
	Content      string   `json:"content"`
	SystemPrompt string   `json:"systemPrompt"`
	Tools        []string `json:"tools"`
	Cwd          string   `json:"cwd"`
	Model        string   `json:"model"`
	MaxTurns     int      `json:"maxTurns"`
}

// fakeRPCProcess implements piRPCProcess for unit tests without spawning a
// real subprocess. Events are scripted inline and emitted on stdout as JSONL
// (one line per event). The request written to stdin is captured for
// assertions.
//
// Pipe topology (matches a real child process):
//
//	stdinW  ← daemon writes prompt to this
//	stdinR  → fake child reads from this
//	stdoutW ← fake child writes JSONL events to this
//	stdoutR → daemon reads JSONL events from this
type fakeRPCProcess struct {
	// Scripted output — either structured events OR raw JSONL lines.
	// When rawLines is non-empty it takes precedence over events.
	events   []map[string]interface{}
	rawLines []string

	// EmitErr, if non-nil, is returned by Wait after stdout is closed.
	// Use it to simulate a non-zero child exit.
	EmitErr error

	started bool

	// stdin pipe (daemon → fake child)
	stdinR *io.PipeReader
	stdinW *io.PipeWriter

	// stdout pipe (fake child → daemon)
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter

	req   *testRPCRequest // decoded from stdin after read
	reqCh chan struct{}   // closed when stdin read completes

	cancel chan struct{} // closed by Kill to signal termination
	doneCh chan struct{} // closed when the handler goroutine exits

	mu sync.Mutex
}

func (p *fakeRPCProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return fmt.Errorf("already started")
	}
	p.started = true

	p.stdinR, p.stdinW = io.Pipe()
	p.stdoutR, p.stdoutW = io.Pipe()

	p.cancel = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.reqCh = make(chan struct{})

	go p.handler()
	return nil
}

// handler simulates the fake child process: read one JSON request from
// stdin, then emit scripted JSONL events on stdout.
func (p *fakeRPCProcess) handler() {
	defer close(p.doneCh)

	// Read stdin on a separate goroutine so a blocked Decode does not
	// prevent the handler from exiting when Kill is called.
	go func() {
		defer close(p.reqCh)
		var req testRPCRequest
		if err := json.NewDecoder(p.stdinR).Decode(&req); err == nil {
			p.mu.Lock()
			p.req = &req
			p.mu.Unlock()
		}
	}()

	// Wait for stdin read to finish OR for abort.
	select {
	case <-p.reqCh:
	case <-p.cancel:
		// Drain reqCh so the stdin goroutine does not leak.
		<-p.reqCh
		// Closing stdoutW without emitting anything signals EOF to the
		// daemon's scanner; it will then call Wait which returns p.EmitErr.
		p.stdoutW.Close()
		return
	}

	// Emit scripted events/lines as JSONL on stdout.
	if len(p.rawLines) > 0 {
		for _, line := range p.rawLines {
			if _, err := fmt.Fprintln(p.stdoutW, line); err != nil {
				break
			}
		}
	} else {
		enc := json.NewEncoder(p.stdoutW)
		for _, ev := range p.events {
			if err := enc.Encode(ev); err != nil {
				break
			}
		}
	}

	// Close stdout to signal EOF to the daemon's scanner.
	p.stdoutW.Close()
}

func (p *fakeRPCProcess) capturedReq() *testRPCRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.req
}

func (p *fakeRPCProcess) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeRPCProcess) Stdout() io.ReadCloser { return p.stdoutR }
func (p *fakeRPCProcess) Wait() error {
	<-p.doneCh
	return p.EmitErr
}
func (p *fakeRPCProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.cancel:
		// already closed
	default:
		close(p.cancel)
	}
	// Close the write end of stdin so a blocked Decode in the handler's
	// stdin goroutine unblocks.
	p.stdinW.Close()
	return nil
}

// ---------------------------------------------------------------------------
// Test helper subprocess: used by the abort test to verify the daemon closes
// stdin (and optionally kills the child) to terminate a long-running RPC
// session. The test binary is re-invoked as a child process.
// ---------------------------------------------------------------------------

const testRPCHelperEnv = "MULTICA_TEST_PI_RPC_HELPER"

func init() {
	if os.Getenv(testRPCHelperEnv) != "1" {
		return
	}
	runTestRPCHelper()
	os.Exit(0)
}

// runTestRPCHelper is invoked by re-running the test binary as a subprocess.
// It reads one JSONL request from stdin, then behaves according to
// MULTICA_TEST_PI_RPC_MODE:
//
//	"abort": emit agent_start, block reading stdin until EOF (the abort
//	         signal the daemon sends by closing stdin), emit agent_end, exit.
func runTestRPCHelper() {
	mode := os.Getenv("MULTICA_TEST_PI_RPC_MODE")

	var req map[string]interface{}
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "helper: decode request: %v\n", err)
		os.Exit(2)
	}

	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(map[string]interface{}{"type": "agent_start"})
	// Flush immediately so the daemon sees the event before we block.
	_ = os.Stdout.Sync()

	switch mode {
	case "abort":
		// Block reading stdin until the daemon closes it.
		buf := make([]byte, 4096)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				break
			}
		}
		_ = enc.Encode(map[string]interface{}{"type": "agent_end"})
	}
}

// newAbortHelperProcess creates a realRPCProcess that runs the current test
// binary in helper mode with MULTICA_TEST_PI_RPC_MODE=mode.
func newAbortHelperProcess(t *testing.T, mode string) *realRPCProcess {
	t.Helper()
	return &realRPCProcess{
		path: os.Args[0],
		// Run just this one test so init() is the only thing that runs.
		args: []string{"-test.run=^TestPiBackend_Execute_RPC_Abort$"},
		env: []string{
			testRPCHelperEnv + "=1",
			"MULTICA_TEST_PI_RPC_MODE=" + mode,
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPiBackend_Execute_RPCMode(t *testing.T) {
	proc := &fakeRPCProcess{
		events: []map[string]interface{}{
			{"type": "agent_start"},
			{"type": "message_update", "delta": "Hello, "},
			{"type": "message_update", "delta": "world!"},
			{"type": "tool_execution_start", "toolName": "read",
				"args": map[string]interface{}{"path": "auth.go"}},
			{"type": "tool_execution_end", "toolName": "read",
				"result": "file contents"},
			{"type": "agent_end"},
		},
	}
	b := &PiBackend{
		rpcProcessFactory: func(context.Context, []string, string, []string) piRPCProcess {
			return proc
		},
	}

	result, err := b.ExecuteRPC(context.Background(), ExecuteRequest{
		Prompt:    "Fix the bug",
		WorkDir:   "/workspace",
		Env:       []string{"FOO=bar"},
		AgentName: "test-agent",
		Model:     "claude-sonnet-4-6",
		Tools:     []string{"read", "write"},
		MaxTurns:  10,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Text assembled by concatenating message_update deltas.
	if result.Output != "Hello, world!" {
		t.Errorf("Output = %q, want %q", result.Output, "Hello, world!")
	}

	// Tool calls collected from start/end pairs.
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "read")
	}
	if !strings.Contains(tc.Input, "auth.go") {
		t.Errorf("ToolCall.Input = %q, want it to contain %q", tc.Input, "auth.go")
	}
	if tc.Output != "file contents" {
		t.Errorf("ToolCall.Output = %q, want %q", tc.Output, "file contents")
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want >= 0", result.DurationMs)
	}

	// Verify the prompt message sent to stdin.
	req := proc.capturedReq()
	if req == nil {
		t.Fatal("no request captured from stdin")
	}
	if req.Type != "prompt" {
		t.Errorf("request.Type = %q, want %q", req.Type, "prompt")
	}
	if req.Content != "Fix the bug" {
		t.Errorf("request.Content = %q, want %q", req.Content, "Fix the bug")
	}
	if req.Cwd != "/workspace" {
		t.Errorf("request.Cwd = %q, want %q", req.Cwd, "/workspace")
	}
	if req.Model != "claude-sonnet-4-6" {
		t.Errorf("request.Model = %q, want %q", req.Model, "claude-sonnet-4-6")
	}
	if len(req.Tools) != 2 || req.Tools[0] != "read" || req.Tools[1] != "write" {
		t.Errorf("request.Tools = %v, want [read write]", req.Tools)
	}
	if req.MaxTurns != 10 {
		t.Errorf("request.MaxTurns = %d, want 10", req.MaxTurns)
	}
}

func TestPiBackend_Execute_RPC_Error(t *testing.T) {
	proc := &fakeRPCProcess{
		events: []map[string]interface{}{
			{"type": "agent_start"},
			{"type": "error", "message": "something went wrong in RPC"},
			{"type": "agent_end"},
		},
	}
	b := &PiBackend{
		rpcProcessFactory: func(context.Context, []string, string, []string) piRPCProcess {
			return proc
		},
	}

	_, err := b.ExecuteRPC(context.Background(), ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	})
	if err == nil {
		t.Fatal("expected error from RPC error event, got nil")
	}
	if !strings.Contains(err.Error(), "something went wrong in RPC") {
		t.Errorf("error should contain RPC error message, got: %v", err)
	}
}

func TestPiBackend_Execute_RPC_Abort(t *testing.T) {
	// Uses a real subprocess (the test binary in helper mode) because
	// aborting is fundamentally about stdin-close signaling to a child
	// process, which a pure in-memory fake cannot faithfully reproduce.
	p := newAbortHelperProcess(t, "abort")
	b := &PiBackend{
		rpcProcessFactory: func(context.Context, []string, string, []string) piRPCProcess {
			return p
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var execErr error
	go func() {
		defer close(done)
		_, execErr = b.ExecuteRPC(ctx, ExecuteRequest{
			Prompt:    "long running task",
			AgentName: "test-agent",
		})
	}()

	// Give the subprocess time to start and emit agent_start.
	time.Sleep(200 * time.Millisecond)

	// Abort via context cancellation — the daemon must close stdin and
	// terminate the child promptly.
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ExecuteRPC did not return after abort within 5s")
	}

	if execErr == nil {
		t.Fatal("expected error from aborted RPC, got nil")
	}
	if !strings.Contains(execErr.Error(), "cancel") {
		t.Errorf("error should mention cancellation, got: %v", execErr)
	}
}

func TestPiBackend_Execute_RPC_MalformedJSON(t *testing.T) {
	// Malformed JSON lines must be skipped; well-formed events around
	// them must still be processed. The third line is not valid JSON.
	proc := &fakeRPCProcess{
		rawLines: []string{
			`{"type":"agent_start"}`,
			`{"type":"message_update","delta":"before "}`,
			`not valid json`,
			`{"type":"message_update","delta":"after"}`,
			`{"type":"agent_end"}`,
		},
	}
	b := &PiBackend{
		rpcProcessFactory: func(context.Context, []string, string, []string) piRPCProcess {
			return proc
		},
	}

	result, err := b.ExecuteRPC(context.Background(), ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "before after" {
		t.Errorf("Output = %q, want %q", result.Output, "before after")
	}
}

func TestPiBackend_Execute_RPC_UnknownEventSkipped(t *testing.T) {
	// Unknown event types must be skipped silently so the daemon is
	// forward-compatible with new pi RPC event types.
	proc := &fakeRPCProcess{
		events: []map[string]interface{}{
			{"type": "agent_start"},
			{"type": "future_event_we_dont_know_about", "data": "ignored"},
			{"type": "message_update", "delta": "ok"},
			{"type": "agent_end"},
		},
	}
	b := &PiBackend{
		rpcProcessFactory: func(context.Context, []string, string, []string) piRPCProcess {
			return proc
		},
	}

	result, err := b.ExecuteRPC(context.Background(), ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "ok" {
		t.Errorf("Output = %q, want %q", result.Output, "ok")
	}
}

func TestPiBackend_Execute_RPC_NoAgentEnd(t *testing.T) {
	// If the process exits without emitting agent_end, ExecuteRPC should
	// still return whatever text was collected (graceful degradation).
	proc := &fakeRPCProcess{
		events: []map[string]interface{}{
			{"type": "agent_start"},
			{"type": "message_update", "delta": "partial output"},
			// no agent_end — child exits anyway
		},
	}
	b := &PiBackend{
		rpcProcessFactory: func(context.Context, []string, string, []string) piRPCProcess {
			return proc
		},
	}

	result, err := b.ExecuteRPC(context.Background(), ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "partial output" {
		t.Errorf("Output = %q, want %q", result.Output, "partial output")
	}
}

func TestBuildPiRPCArgs(t *testing.T) {
	// The RPC invocation must pass --mode rpc and default the binary
	// name to "pi" when no override is provided.
	args := buildPiRPCArgs(&PiBackend{})
	if len(args) < 2 || args[0] != "--mode" || args[1] != "rpc" {
		t.Errorf("args = %v, want [--mode rpc ...]", args)
	}
}
