package mcp

// coverage_gap6_test.go — sixth pass at uncovered branches.
// Targets: handleWebhookTool pool paths (mock WebhookJobQueue), handleDynamicContentTool
// missing type_name/id/status args in get/list/update/set_content_status operations.

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// ---------------------------------------------------------------------------
// mockWebhookPool — in-package mock of WebhookJobQueue for pool-path tests
// ---------------------------------------------------------------------------

type mockWebhookPool struct {
	logs      []smeldr.DeliveryLog
	jobs      []smeldr.OutboundJob
	lastAt    *time.Time
	retryErr  error
	logsErr   error
	jobsErr   error
	statsErr  error
}

func (m *mockWebhookPool) Enqueue(_ context.Context, _ smeldr.OutboundJob) error { return nil }
func (m *mockWebhookPool) RetryDead(_ context.Context, _ string) error           { return m.retryErr }
func (m *mockWebhookPool) ListDeliveryLogs(_ context.Context, _ string) ([]smeldr.DeliveryLog, error) {
	return m.logs, m.logsErr
}
func (m *mockWebhookPool) ListJobsForEndpoint(_ context.Context, _ string) ([]smeldr.OutboundJob, error) {
	return m.jobs, m.jobsErr
}
func (m *mockWebhookPool) DeliveryStats(_ context.Context, _ string) (total, success, failed int, lastAttempt *time.Time, err error) {
	return 2, 1, 1, m.lastAt, m.statsErr
}

// ---------------------------------------------------------------------------
// handleWebhookTool — pool-nil paths (-32603)
// ---------------------------------------------------------------------------

// TestWebhookTool_ListDeliveries_PoolNil verifies -32603 when the webhook pool is nil.
func TestWebhookTool_ListDeliveries_PoolNil(t *testing.T) {
	srv, _ := newWebhookServer(t)
	srv.webhookPool = nil // clear the pool to reach the "not configured" branch
	_, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{
		"job_id": "some-job",
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("expected -32603 (pool nil), got %v", rpcErr)
	}
}

// TestWebhookTool_RetryWebhook_PoolNil verifies -32603 when the webhook pool is nil.
func TestWebhookTool_RetryWebhook_PoolNil(t *testing.T) {
	srv, _ := newWebhookServer(t)
	srv.webhookPool = nil
	_, rpcErr := callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{
		"job_id": "some-job",
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("expected -32603 (pool nil), got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleWebhookTool — pool-present paths via mockWebhookPool
// ---------------------------------------------------------------------------

// TestWebhookTool_ListDeliveries_NoIDArg verifies -32602 when neither job_id nor
// endpoint_id is provided (but the pool IS configured).
func TestWebhookTool_ListDeliveries_NoIDArg(t *testing.T) {
	srv, _ := newWebhookServer(t)
	srv.webhookPool = &mockWebhookPool{}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 (no id), got %v", rpcErr)
	}
}

// TestWebhookTool_ListDeliveries_ByJobID_NilLogs verifies nil logs are coerced
// to an empty slice and returned successfully.
func TestWebhookTool_ListDeliveries_ByJobID_NilLogs(t *testing.T) {
	srv, _ := newWebhookServer(t)
	srv.webhookPool = &mockWebhookPool{logs: nil}
	res, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{
		"job_id": "j-001",
	})
	if rpcErr != nil {
		t.Fatalf("list_webhook_deliveries by job_id: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	logs, _ := fields["logs"].([]any)
	if logs == nil {
		t.Error("expected empty logs slice, got nil")
	}
}

// TestWebhookTool_ListDeliveries_ByEndpointID verifies listing by endpoint_id
// (the jobs path) returns jobs when the pool is present.
func TestWebhookTool_ListDeliveries_ByEndpointID_NilJobs(t *testing.T) {
	srv, _ := newWebhookServer(t)
	srv.webhookPool = &mockWebhookPool{jobs: nil}
	res, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{
		"endpoint_id": "ep-001",
	})
	if rpcErr != nil {
		t.Fatalf("list_webhook_deliveries by endpoint_id: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	jobs, _ := fields["jobs"].([]any)
	if jobs == nil {
		t.Error("expected empty jobs slice, got nil")
	}
}

// TestWebhookTool_RetryWebhook_Success verifies retry_webhook succeeds when the
// pool is present and RetryDead returns no error.
func TestWebhookTool_RetryWebhook_Success(t *testing.T) {
	srv, _ := newWebhookServer(t)
	srv.webhookPool = &mockWebhookPool{}
	res, rpcErr := callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{
		"job_id": "dead-job-001",
	})
	if rpcErr != nil {
		t.Fatalf("retry_webhook: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["retried"] != true {
		t.Errorf("retried = %v, want true", fields["retried"])
	}
}

// TestWebhookTool_ListWebhooks_WithPoolStats verifies that list_webhooks
// includes the lastAt timestamp when the pool reports a non-nil last attempt.
func TestWebhookTool_ListWebhooks_WithPoolStats(t *testing.T) {
	srv, store := newWebhookServer(t)
	ctx := newAdminCtx()

	// Create an endpoint so list_webhooks has something to process.
	_, rpcErr := callTool(t, srv, ctx, "create_webhook", map[string]any{
		"url":    "https://example.com/hookpool",
		"events": []any{"post.published"},
	})
	if rpcErr != nil {
		t.Fatalf("create_webhook: %v", rpcErr.Message)
	}
	_ = store

	last := time.Now().UTC()
	srv.webhookPool = &mockWebhookPool{lastAt: &last}

	res, rpcErr := callTool(t, srv, ctx, "list_webhooks", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_webhooks with pool stats: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	webhooks, _ := fields["webhooks"].([]any)
	if len(webhooks) != 1 {
		t.Fatalf("got %d webhooks, want 1", len(webhooks))
	}
	ep, _ := webhooks[0].(map[string]any)
	if ep["last_attempt_at"] == "" || ep["last_attempt_at"] == nil {
		t.Errorf("last_attempt_at should be set, got %v", ep["last_attempt_at"])
	}
}

// ---------------------------------------------------------------------------
// handleDynamicContentTool — missing required args in get/list/update/set operations
// ---------------------------------------------------------------------------

// TestDynamicTool_GetContent_MissingTypeName verifies -32602 when type_name absent.
func TestDynamicTool_GetContent_MissingTypeName(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "get_content", map[string]any{
		"slug": "some-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing type_name in get_content, got %v", rpcErr)
	}
}

// TestDynamicTool_ListContent_MissingTypeName verifies -32602 when type_name absent.
func TestDynamicTool_ListContent_MissingTypeName(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "list_content", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing type_name in list_content, got %v", rpcErr)
	}
}

// TestDynamicTool_UpdateContent_MissingTypeName verifies -32602 when type_name absent.
func TestDynamicTool_UpdateContent_MissingTypeName(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "update_content", map[string]any{
		"id": "some-id",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing type_name in update_content, got %v", rpcErr)
	}
}

// TestDynamicTool_UpdateContent_MissingID verifies -32602 when id absent in update_content.
func TestDynamicTool_UpdateContent_MissingID(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "blog")
	_, rpcErr := callTool(t, srv, newAdminCtx(), "update_content", map[string]any{
		"type_name": "blog",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id in update_content, got %v", rpcErr)
	}
}

// TestDynamicTool_SetContentStatus_MissingTypeName verifies -32602.
func TestDynamicTool_SetContentStatus_MissingTypeName(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "set_content_status", map[string]any{
		"id":     "some-id",
		"status": "published",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing type_name in set_content_status, got %v", rpcErr)
	}
}

// TestDynamicTool_SetContentStatus_MissingID verifies -32602.
func TestDynamicTool_SetContentStatus_MissingID(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "blog2")
	_, rpcErr := callTool(t, srv, newAdminCtx(), "set_content_status", map[string]any{
		"type_name": "blog2",
		"status":    "published",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id in set_content_status, got %v", rpcErr)
	}
}

// TestDynamicTool_SetContentStatus_MissingStatus verifies -32602.
func TestDynamicTool_SetContentStatus_MissingStatus(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "blog3")
	_, rpcErr := callTool(t, srv, newAdminCtx(), "set_content_status", map[string]any{
		"type_name": "blog3",
		"id":        "some-id",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing status in set_content_status, got %v", rpcErr)
	}
}

// TestDynamicTool_SetContentStatus_InvalidStatus verifies -32602 for unknown status value.
func TestDynamicTool_SetContentStatus_InvalidStatus(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "blog4")
	_, rpcErr := callTool(t, srv, newAdminCtx(), "set_content_status", map[string]any{
		"type_name": "blog4",
		"id":        "some-id",
		"status":    "invalid_status_xyz",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for invalid status, got %v", rpcErr)
	}
}

// TestDynamicTool_ListContent_Success verifies list_content succeeds and
// covers the items == nil nil-guard branch (empty result from a fresh type).
func TestDynamicTool_ListContent_Success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "widget")
	res, rpcErr := callTool(t, srv, newAdminCtx(), "list_content", map[string]any{
		"type_name": "widget",
	})
	if rpcErr != nil {
		t.Fatalf("list_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["items"] == nil {
		t.Error("items should not be nil")
	}
}
