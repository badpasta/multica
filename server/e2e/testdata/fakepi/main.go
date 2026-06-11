// fakepi is a minimal stand-in for the Pi CLI used by e2e tests of the
// Multica pi backend. It is not a general-purpose fake: it only implements
// the exact shapes the daemon sends (print mode: `-p ... <prompt>` on the
// command line; RPC mode: `--mode rpc` with one JSON prompt line on stdin)
// and emits a small fixed event stream on stdout.
//
// Dispatch is controlled by the MULTICA_FAKE_PI_MODE environment variable:
//
//	"print"       — write a fixed text line to stdout and exit 0.
//	"rpc"         — read one JSON line from stdin, emit RPC events, exit 0.
//	"rpc_error"   — emit an RPC error event and exit 0.
//	"block"       — sleep forever; used to exercise daemon-side timeouts.
//	"fail"        — write a stderr message and exit 1.
//	"" (unset)    — default to print.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// debug log, if MULTICA_FAKE_PI_LOG is set, writes to that path for
// post-mortem inspection by tests.
var dlog *log.Logger

func init() {
	if path := os.Getenv("MULTICA_FAKE_PI_LOG"); path != "" {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err == nil {
			dlog = log.New(f, "", log.LstdFlags)
		}
	}
	if dlog == nil {
		dlog = log.New(os.Stderr, "[fakepi] ", 0)
	}
}

func main() {
	dlog.Printf("start args=%q", os.Args)
	dlog.Printf("start mode=%q", os.Getenv("MULTICA_FAKE_PI_MODE"))
	mode := os.Getenv("MULTICA_FAKE_PI_MODE")
	if mode == "" {
		mode = "print"
	}

	switch mode {
	case "print":
		// Print mode: the Pi CLI writes its result to stdout as plain text.
		dlog.Printf("print: writing to stdout")
		fmt.Println("Hello from pi!")
		dlog.Printf("print: done")

	case "rpc":
		// RPC mode: read the JSON prompt from stdin (one line), then emit
		// a scripted sequence of events on stdout. The daemon keeps stdin
		// open after the prompt, so we must not try to read past the first
		// line — doing so would hang until the daemon kills us.
		dlog.Printf("rpc: reading stdin")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			dlog.Printf("rpc: got line len=%d", len(scanner.Bytes()))
			// Verify the prompt message is well-formed JSON. We don't
			// inspect fields — just confirm it parses as an object.
			var req map[string]interface{}
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				fmt.Fprintf(os.Stderr, "fakepi: invalid prompt JSON: %v\n", err)
				dlog.Printf("rpc: invalid JSON: %v", err)
				os.Exit(2)
			}
			dlog.Printf("rpc: JSON parsed ok")
		} else {
			dlog.Printf("rpc: scanner.Scan returned false: %v", scanner.Err())
		}

		enc := json.NewEncoder(os.Stdout)
		events := []map[string]interface{}{
			{"type": "agent_start"},
			{"type": "turn_start"},
			{"type": "message_start"},
			{"type": "message_update", "delta": "I'll help "},
			{"type": "message_update", "delta": "you with that."},
			{"type": "message_end"},
			{"type": "tool_execution_start", "toolName": "read",
				"args": map[string]interface{}{"path": "test.go"}},
			{"type": "tool_execution_end", "toolName": "read",
				"result": "file contents"},
			{"type": "turn_end"},
			{"type": "agent_end"},
		}
		dlog.Printf("rpc: encoding %d events", len(events))
		for i, ev := range events {
			if err := enc.Encode(ev); err != nil {
				fmt.Fprintf(os.Stderr, "fakepi: encode event %d: %v\n", i, err)
				dlog.Printf("rpc: encode err at %d: %v", i, err)
				os.Exit(3)
			}
		}
		dlog.Printf("rpc: events written, exiting")

	case "rpc_error":
		// RPC mode that signals a protocol-level error.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			var req map[string]interface{}
			_ = json.Unmarshal(scanner.Bytes(), &req)
		}
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]interface{}{"type": "agent_start"})
		_ = enc.Encode(map[string]interface{}{
			"type": "error", "message": "something went wrong in pi",
		})
		_ = enc.Encode(map[string]interface{}{"type": "agent_end"})

	case "block":
		// Block until killed. Used by the timeout test to verify the
		// daemon imposes its own deadline on a hung child.
		// time.Sleep keeps the main goroutine parked without tripping
		// the runtime's deadlock detector (which fires on bare
		// `select {}` or receive-from-never-closed-channel patterns).
		time.Sleep(24 * time.Hour)

	case "fail":
		fmt.Fprintln(os.Stderr, "pi: simulated failure")
		os.Exit(1)

	default:
		fmt.Fprintf(os.Stderr, "fakepi: unknown mode %q\n", mode)
		os.Exit(2)
	}
}
