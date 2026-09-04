package mcp

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"smeldr.dev/core"
)

// captureSlog swaps slog's default logger for one writing to a buffer at
// the given level (so Debug-level lines are visible when needed), runs fn,
// restores the previous default, and returns the captured text.
func captureSlog(t *testing.T, level slog.Level, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

// TestHandleToolMethod_ToolsCall_LogsSuccess verifies a successful
// tools/call produces an Info-level log line naming the tool and actor.
func TestHandleToolMethod_ToolsCall_LogsSuccess(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	ctx := newAuthorCtx()

	params, err := json.Marshal(map[string]any{
		"name":      "list_type_tools",
		"arguments": map[string]any{"type_name": "test_mcp_post"},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	out := captureSlog(t, slog.LevelInfo, func() {
		resp, handled := srv.handleToolMethod(ctx, jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Method:  "tools/call",
			Params:  params,
		})
		if !handled {
			t.Fatal("handleToolMethod(tools/call) returned handled=false, want true")
		}
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
	})

	if !strings.Contains(out, "tools/call") {
		t.Errorf("log missing event name:\n%s", out)
	}
	if !strings.Contains(out, "list_type_tools") {
		t.Errorf("log missing tool name:\n%s", out)
	}
	if strings.Contains(out, "test_mcp_post") {
		t.Errorf("log should not contain call arguments (leak-surface risk), got:\n%s", out)
	}
}

// TestHandleToolMethod_ToolsCall_LogsFailure verifies a failing tools/call
// (unknown tool name) produces a Warn-level log line naming the tool,
// without leaking arguments.
func TestHandleToolMethod_ToolsCall_LogsFailure(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	ctx := newAuthorCtx()

	params, err := json.Marshal(map[string]any{
		"name":      "definitely_not_a_real_tool",
		"arguments": map[string]any{"secret_field": "should-not-appear-in-logs"},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	out := captureSlog(t, slog.LevelInfo, func() {
		resp, handled := srv.handleToolMethod(ctx, jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Method:  "tools/call",
			Params:  params,
		})
		if !handled {
			t.Fatal("handleToolMethod(tools/call) returned handled=false, want true")
		}
		if resp.Error == nil {
			t.Fatal("expected an error for an unknown tool name")
		}
	})

	if !strings.Contains(out, "WARN") {
		t.Errorf("expected a WARN-level line, got:\n%s", out)
	}
	if !strings.Contains(out, "definitely_not_a_real_tool") {
		t.Errorf("log missing tool name:\n%s", out)
	}
	if strings.Contains(out, "should-not-appear-in-logs") {
		t.Errorf("log should not contain call arguments (leak-surface risk), got:\n%s", out)
	}
}

// TestHandleToolMethod_ToolsList_LogsAtDebug verifies tools/list logs at
// Debug level — visible only when the level is lowered, not at Info.
func TestHandleToolMethod_ToolsList_LogsAtDebug(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	ctx := newAuthorCtx()
	req := jsonRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}

	infoOut := captureSlog(t, slog.LevelInfo, func() {
		if _, handled := srv.handleToolMethod(ctx, req); !handled {
			t.Fatal("handleToolMethod(tools/list) returned handled=false, want true")
		}
	})
	if strings.Contains(infoOut, "tools/list") {
		t.Errorf("tools/list should not log at Info level, got:\n%s", infoOut)
	}

	debugOut := captureSlog(t, slog.LevelDebug, func() {
		if _, handled := srv.handleToolMethod(ctx, req); !handled {
			t.Fatal("handleToolMethod(tools/list) returned handled=false, want true")
		}
	})
	if !strings.Contains(debugOut, "tools/list") {
		t.Errorf("tools/list should log at Debug level, got:\n%s", debugOut)
	}
}

// TestToolCallName verifies the params-name extraction helper directly,
// including its graceful degrade on unparseable input.
func TestToolCallName(t *testing.T) {
	if got := toolCallName(json.RawMessage(`{"name":"create_post","arguments":{}}`)); got != "create_post" {
		t.Errorf("toolCallName = %q, want create_post", got)
	}
	if got := toolCallName(json.RawMessage(`{bad json`)); got != "" {
		t.Errorf("toolCallName(invalid) = %q, want empty string", got)
	}
	if got := toolCallName(json.RawMessage(`{}`)); got != "" {
		t.Errorf("toolCallName(no name field) = %q, want empty string", got)
	}
}
