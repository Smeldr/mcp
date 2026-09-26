package mcp

// Tests for validateKnownArgs, the shared unknown-parameter rejection pass
// added to handleToolsCall for every create_*/update_* tool (01a0da88-2).
// Closes the silent-drop gap found live 2026-09-25: create_signal called
// with "body" instead of "message", four times, always succeeding with an
// empty field.

import (
	"strings"
	"testing"
)

// TestValidateKnownArgs_UnknownKey_Rejected reproduces the exact incident
// this Task exists to close: create_signal called with "body" instead of
// "message" must now fail loudly instead of silently succeeding.
func TestValidateKnownArgs_UnknownKey_Rejected(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"signal_type": "plan-ready",
		"body":        "this should be message, not body",
	})
	if rpcErr == nil {
		t.Fatal("expected error for unknown parameter \"body\", got nil")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
	if !strings.Contains(rpcErr.Message, "unknown parameter: body") {
		t.Errorf("message = %q, want it to mention \"unknown parameter: body\"", rpcErr.Message)
	}
}

// TestValidateKnownArgs_KnownKeysOnly_Passes verifies that a call using only
// declared parameter names is unaffected by the new check.
func TestValidateKnownArgs_KnownKeysOnly_Passes(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
		"message":     "the real field",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal with only known keys: %v", rpcErr.Message)
	}
}

// TestValidateKnownArgs_NonCreateUpdateTool_Skipped verifies the check is
// scoped to create_*/update_* tools only, per the Task's own text - a
// non-create/update tool with an extraneous key is not rejected by this
// check (it may still fail downstream for unrelated reasons, but not with
// "unknown parameter").
func TestValidateKnownArgs_NonCreateUpdateTool_Skipped(t *testing.T) {
	srv := newSignalServer(t)
	seedSignalAt(t, srv, "first", "2026-09-20T10:00:00Z")
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_signals", map[string]any{
		"receiver":       "architect",
		"not_a_real_key": "whatever",
	})
	if rpcErr != nil {
		t.Errorf("list_signals with an extraneous key: expected no error from validateKnownArgs, got %v", rpcErr)
	}
}

// TestValidateKnownArgs_DynamicContentFieldsNotValidatedByThisCheck verifies
// the check is shallow: it only validates create_content's own top-level
// keys (type_name, fields) - the fields map's own type-specific sub-keys are
// governed entirely by the dynamic content type's own registered schema
// (found live while writing this test: an unknown nested field is indeed
// rejected, but by that separate, pre-existing schema-validation mechanism,
// not by validateKnownArgs). A legitimate nested field must pass cleanly.
func TestValidateKnownArgs_DynamicContentFieldsNotValidatedByThisCheck(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "widget")
	_, rpcErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "widget",
		"fields":    map[string]any{"title": "a title"},
	})
	if rpcErr != nil {
		t.Errorf("create_content with a legitimate nested field: expected success, got %v", rpcErr)
	}
}

// TestValidateKnownArgs_DynamicContentTopLevelKeyRejected verifies that an
// unknown *top-level* key on create_content (as opposed to a nested "fields"
// sub-key) is still caught, proving the shallow check does apply at the
// level it's meant to.
func TestValidateKnownArgs_DynamicContentTopLevelKeyRejected(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "widget")
	_, rpcErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name":     "widget",
		"fields":        map[string]any{"title": "a title"},
		"extra_top_lvl": "not a real create_content parameter",
	})
	if rpcErr == nil {
		t.Fatal("expected error for unknown top-level parameter, got nil")
	}
	if rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "unknown parameter") {
		t.Errorf("expected -32602 unknown parameter, got %v", rpcErr)
	}
}

// TestValidateKnownArgs_UnknownToolName_NoOp verifies that a create_-prefixed
// name with no matching tool definition falls through to the existing
// "unknown tool" error path unchanged, rather than validateKnownArgs itself
// producing a confusing error.
func TestValidateKnownArgs_UnknownToolName_NoOp(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_totally_unregistered_type", map[string]any{
		"whatever": "x",
	})
	if rpcErr == nil {
		t.Fatal("expected an error for an unregistered create_ tool, got nil")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602 (unknown tool)", rpcErr.Code)
	}
	if strings.Contains(rpcErr.Message, "unknown parameter") {
		t.Errorf("message = %q, should not be validateKnownArgs's own error for a tool it has no definition for", rpcErr.Message)
	}
}
