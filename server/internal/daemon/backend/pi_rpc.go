package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// RPC event type constants emitted by the pi CLI on stdout in RPC mode.
const (
	RPCEventAgentStart         = "agent_start"
	RPCEventAgentEnd           = "agent_end"
	RPCEventTurnStart          = "turn_start"
	RPCEventTurnEnd            = "turn_end"
	RPCEventMessageStart       = "message_start"
	RPCEventMessageUpdate      = "message_update"
	RPCEventMessageEnd         = "message_end"
	RPCEventToolExecutionStart = "tool_execution_start"
	RPCEventToolExecutionEnd   = "tool_execution_end"
	RPCEventError              = "error"
)

// piRPCEvent is the common envelope for all RPC events from pi.
// Only the Type field is required; payload fields depend on the event type.
type piRPCEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta,omitempty"`
	ToolName string          `json:"toolName,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	Result   string          `json:"result,omitempty"`
	Message  string          `json:"message,omitempty"`
}

// piRPCPromptMessage is the JSON message the daemon sends to pi's stdin
// to start an RPC session.
type piRPCPromptMessage struct {
	Type         string   `json:"type"`
	Content      string   `json:"content"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Cwd          string   `json:"cwd,omitempty"`
	Model        string   `json:"model,omitempty"`
	MaxTurns     int      `json:"maxTurns,omitempty"`
}

// piRPCProcess abstracts a long-running pi subprocess that communicates via
// stdin/stdout JSONL. Tests supply a fake; production uses realRPCProcess.
type piRPCProcess interface {
	Start() error
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
}

// realRPCProcess wraps an exec.Cmd for use as a piRPCProcess.
type realRPCProcess struct {
	path string
	args []string
	dir  string
	env  []string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *realRPCProcess) Start() error {
	p.cmd = exec.Command(p.path, p.args...)
	if p.dir != "" {
		p.cmd.Dir = p.dir
	}
	if len(p.env) > 0 {
		p.cmd.Env = p.env
	}
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	p.stdin = stdin
	p.stdout = stdout
	if err := p.cmd.Start(); err != nil {
		return err
	}
	return nil
}

func (p *realRPCProcess) Stdin() io.WriteCloser  { return p.stdin }
func (p *realRPCProcess) Stdout() io.ReadCloser  { return p.stdout }
func (p *realRPCProcess) Wait() error            { return p.cmd.Wait() }
func (p *realRPCProcess) Kill() error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

// ExecuteRPC runs the Pi CLI in RPC mode (Mode B) where the daemon and pi
// communicate over stdin/stdout using a JSONL protocol.
//
// Protocol:
//
//	daemon → pi stdin:
//	  {"type":"prompt","content":"...","systemPrompt":"...","tools":[...],"cwd":"...","model":"...","maxTurns":N}
//
//	pi → daemon stdout (one JSON object per line):
//	  {"type":"agent_start"}
//	  {"type":"turn_start"}
//	  {"type":"message_start"}
//	  {"type":"message_update","delta":"I'll read the file."}
//	  {"type":"message_end"}
//	  {"type":"tool_execution_start","toolName":"read","args":{...}}
//	  {"type":"tool_execution_end","toolName":"read","result":"..."}
//	  {"type":"turn_end"}
//	  {"type":"agent_end"}
//	  {"type":"error","message":"..."}  // on protocol/runtime errors
//
// Text is assembled from message_update.delta concatenation. Tool calls are
// paired from tool_execution_start and tool_execution_end. An "error" event
// causes the run to fail with that message. agent_end or stdout-EOF
// terminates the session gracefully. Context cancellation aborts the run.
func (p *PiBackend) ExecuteRPC(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	binaryName := p.binaryPath
	if binaryName == "" {
		binaryName = "pi"
	}

	args := buildPiRPCArgs(p)

	var proc piRPCProcess
	if p.rpcProcessFactory != nil {
		// Test-supplied factory: skip LookPath so tests run without pi installed.
		proc = p.rpcProcessFactory(ctx, args, req.WorkDir, req.Env)
	} else {
		lookedUp, err := exec.LookPath(binaryName)
		if err != nil {
			return ExecuteResult{}, fmt.Errorf(
				"pi executable not found at %q: %w — install Pi from https://github.com/canopyprotocol/pi and ensure it is on your PATH",
				binaryName, err,
			)
		}
		proc = &realRPCProcess{
			path: lookedUp,
			args: args,
			dir:  req.WorkDir,
			env:  req.Env,
		}
	}

	if err := proc.Start(); err != nil {
		return ExecuteResult{}, fmt.Errorf("failed to start pi RPC process: %w", err)
	}

	start := time.Now()

	// Send the prompt message to stdin. stdin is deliberately kept open
	// after the prompt so the child can distinguish "no more prompts" from
	// "session aborted". exec.Cmd.Wait() closes it automatically when the
	// child exits; on abort, Kill() terminates the child first.
	stdin := proc.Stdin()
	promptMsg := buildRPCPromptMessage(req, p.Config)
	if err := json.NewEncoder(stdin).Encode(promptMsg); err != nil {
		_ = stdin.Close()
		_ = proc.Kill()
		_ = proc.Wait()
		return ExecuteResult{}, fmt.Errorf("failed to write prompt to pi stdin: %w", err)
	}
	// stdin stays open; Wait() closes it after the child exits.

	// Watch for context cancellation in the background; abort the child
	// process promptly so a hung pi does not block the daemon forever.
	stopAbort := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = proc.Kill()
		case <-stopAbort:
		}
	}()

	// Parse JSONL events from stdout.
	var output strings.Builder
	var toolCalls []ToolCall
	var pendingTool *ToolCall
	var rpcErr error

	scanner := bufio.NewScanner(proc.Stdout())
	// Allow up to 1MB per line — tool results can be large.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev piRPCEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Malformed JSON lines are skipped — pi may emit diagnostics
			// or progress lines that aren't JSON.
			continue
		}

		switch ev.Type {
		case RPCEventMessageUpdate:
			output.WriteString(ev.Delta)
		case RPCEventToolExecutionStart:
			pendingTool = &ToolCall{
				Name:  ev.ToolName,
				Input: string(ev.Args),
			}
		case RPCEventToolExecutionEnd:
			if pendingTool != nil && pendingTool.Name == ev.ToolName {
				pendingTool.Output = ev.Result
				toolCalls = append(toolCalls, *pendingTool)
			}
			pendingTool = nil
		case RPCEventError:
			rpcErr = fmt.Errorf("pi RPC error: %s", ev.Message)
		case RPCEventAgentEnd:
			// Graceful termination signal — stop reading further events.
			goto done
		case RPCEventAgentStart, RPCEventTurnStart, RPCEventTurnEnd,
			RPCEventMessageStart, RPCEventMessageEnd:
			// Lifecycle / envelope events — no payload we need to capture.
		default:
			// Unknown event types are silently ignored for forward
			// compatibility with future pi RPC protocol extensions.
		}
	}
done:
	// Stop the abort goroutine before Wait.
	close(stopAbort)

	waitErr := proc.Wait()
	duration := time.Since(start)

	result := ExecuteResult{
		Output:     output.String(),
		ToolCalls:  toolCalls,
		DurationMs: duration.Milliseconds(),
	}

	// Priority: RPC protocol error > process error > scanner error.
	if rpcErr != nil {
		return result, rpcErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("pi RPC execution cancelled: %w", ctx.Err())
		}
		return result, fmt.Errorf("pi RPC process exited with error: %w", waitErr)
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("pi RPC stdout read error: %w", err)
	}

	return result, nil
}

// buildPiRPCArgs constructs the argument list for an RPC-mode Pi invocation.
func buildPiRPCArgs(p *PiBackend) []string {
	_ = p // reserved for future per-backend flags (e.g. --thinking)
	return []string{"--mode", "rpc"}
}

// buildRPCPromptMessage composes the JSON prompt message sent to pi on
// stdin to start an RPC session.
func buildRPCPromptMessage(req ExecuteRequest, cfg PiConfig) piRPCPromptMessage {
	_ = cfg // reserved for future pi-specific prompt fields
	return piRPCPromptMessage{
		Type:     "prompt",
		Content:  req.Prompt,
		Tools:    req.Tools,
		Cwd:      req.WorkDir,
		Model:    req.Model,
		MaxTurns: req.MaxTurns,
	}
}
