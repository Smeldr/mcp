package mcp

import (
	"context"
	"database/sql"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

const webhookTestSecret = "test-secret-32-bytes-xxxxxxxxxxxx"

// newWebhookServer creates an in-memory SQLite-backed server with webhook
// tools enabled. It wires the store through app.Webhooks so the server picks
// it up automatically in New().
func newWebhookServer(t *testing.T) (*Server, *smeldr.WebhookStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create webhook table: %v", err)
	}

	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(webhookTestSecret),
		DB:      db,
	})
	store := smeldr.NewWebhookStore(db, []byte(webhookTestSecret))
	app.Webhooks(store)
	return New(app), store
}

// TestWebhookTools_Presence verifies all 5 webhook tools appear in tools/list
// when the app has a webhook store configured.
func TestWebhookTools_Presence(t *testing.T) {
	srv, _ := newWebhookServer(t)
	result := srv.handleToolsList(newAdminCtx())
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	want := []string{"create_webhook", "list_webhooks", "delete_webhook", "list_webhook_deliveries", "retry_webhook"}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing webhook tool %q", w)
		}
	}
}

// TestWebhookTools_Absence verifies no webhook tools appear when no store is configured.
func TestWebhookTools_Absence(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(webhookTestSecret),
	})
	srv := New(app)
	result := srv.handleToolsList(newAdminCtx())
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isWebhookTool(tool.Name) {
			t.Errorf("webhook tool %q present without webhook store", tool.Name)
		}
	}
}

// TestIsWebhookTool verifies the predicate for all 5 tool names and one non-tool.
func TestIsWebhookTool(t *testing.T) {
	for _, name := range []string{"create_webhook", "list_webhooks", "delete_webhook", "list_webhook_deliveries", "retry_webhook"} {
		if !isWebhookTool(name) {
			t.Errorf("isWebhookTool(%q) = false, want true", name)
		}
	}
	if isWebhookTool("create_blog_post") {
		t.Error("isWebhookTool(create_blog_post) = true, want false")
	}
}

// TestWebhookTools_AuthorDenied verifies Author role cannot call webhook tools (Admin required).
func TestWebhookTools_AuthorDenied(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_webhook", map[string]any{
		"url":    "https://example.com/hook",
		"events": []any{"post.published"},
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for Author, got %v", rpcErr)
	}
}

// TestWebhookTools_CreateWebhook_success creates a webhook and returns the secret.
func TestWebhookTools_CreateWebhook_success(t *testing.T) {
	srv, _ := newWebhookServer(t)
	res, rpcErr := callTool(t, srv, newAdminCtx(), "create_webhook", map[string]any{
		"url":    "https://example.com/hook",
		"events": []any{"post.published"},
	})
	if rpcErr != nil {
		t.Fatalf("create_webhook: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["id"] == nil || fields["id"] == "" {
		t.Error("create_webhook returned empty id")
	}
	if fields["secret"] == nil || fields["secret"] == "" {
		t.Error("create_webhook returned empty secret")
	}
}

// TestWebhookTools_CreateWebhook_missingURL verifies missing url returns -32602.
func TestWebhookTools_CreateWebhook_missingURL(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "create_webhook", map[string]any{
		"events": []any{"post.published"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing url, got %v", rpcErr)
	}
}

// TestWebhookTools_CreateWebhook_emptyEvents verifies empty events array returns -32602.
func TestWebhookTools_CreateWebhook_emptyEvents(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "create_webhook", map[string]any{
		"url":    "https://example.com/hook",
		"events": []any{},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for empty events, got %v", rpcErr)
	}
}

// TestWebhookTools_ListWebhooks verifies list_webhooks returns the created endpoint.
func TestWebhookTools_ListWebhooks(t *testing.T) {
	srv, _ := newWebhookServer(t)
	ctx := newAdminCtx()

	callTool(t, srv, ctx, "create_webhook", map[string]any{
		"url":    "https://example.com/hook1",
		"events": []any{"post.published"},
	})

	res, rpcErr := callTool(t, srv, ctx, "list_webhooks", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_webhooks: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	webhooks, _ := fields["webhooks"].([]any)
	if len(webhooks) != 1 {
		t.Errorf("got %d webhooks, want 1", len(webhooks))
	}
}

// TestWebhookTools_DeleteWebhook verifies delete_webhook removes an endpoint.
func TestWebhookTools_DeleteWebhook(t *testing.T) {
	srv, _ := newWebhookServer(t)
	ctx := newAdminCtx()

	createRes, _ := callTool(t, srv, ctx, "create_webhook", map[string]any{
		"url":    "https://example.com/hook2",
		"events": []any{"post.published"},
	})
	id, _ := unwrapToolResult(t, createRes)["id"].(string)

	_, rpcErr := callTool(t, srv, ctx, "delete_webhook", map[string]any{"id": id})
	if rpcErr != nil {
		t.Fatalf("delete_webhook: %v", rpcErr.Message)
	}

	res, _ := callTool(t, srv, ctx, "list_webhooks", map[string]any{})
	fields := unwrapToolResult(t, res)
	webhooks, _ := fields["webhooks"].([]any)
	if len(webhooks) != 0 {
		t.Errorf("got %d webhooks after delete, want 0", len(webhooks))
	}
}

// TestWebhookTools_DeleteWebhook_missingID verifies missing id returns -32602.
func TestWebhookTools_DeleteWebhook_missingID(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "delete_webhook", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id, got %v", rpcErr)
	}
}

// TestWebhookTools_ListDeliveries_noPool verifies list_webhook_deliveries returns
// -32603 when no webhook pool is configured.
func TestWebhookTools_ListDeliveries_noPool(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{
		"job_id": "some-job-id",
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("expected -32603 (no pool), got %v", rpcErr)
	}
}

// TestWebhookTools_RetryWebhook_noPool verifies retry_webhook returns -32603
// when no webhook pool is configured.
func TestWebhookTools_RetryWebhook_noPool(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{
		"job_id": "some-job-id",
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("expected -32603 (no pool), got %v", rpcErr)
	}
}

// retry_webhook against core's own pool (core T1266): a dead and a pending job
// are queued again, a delivered job is refused with webhook.job_delivered and
// its status, and an id that names no job with webhook.job_not_found. The App
// is not run, so no worker delivers anything.
func TestWebhookTools_RetryWebhook_corePool(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(webhookTestSecret), DB: db})
	app.Webhooks(smeldr.NewWebhookStore(db, []byte(webhookTestSecret)))
	srv := New(app)
	now := time.Now().UTC()
	for id, status := range map[string]string{"dead-job": "dead", "pending-job": "pending", "delivered-job": "delivered"} {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO smeldr_outbound_jobs (id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status)
			VALUES ($1, 'ep', 'https://example.com/hook', 'enc', '{}', 'post.created', 3, $2, $2, $3, $4)`, id, now.Add(time.Hour), now.Add(2*time.Hour), status); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"dead-job", "pending-job"} {
		res, rpcErr := callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{"job_id": id})
		if rpcErr != nil {
			t.Fatalf("retry %s: %s", id, rpcErr.Message)
		}
		if got := unwrapToolResult(t, res); got["retried"] != true || got["job_id"] != id {
			t.Errorf("retry %s = %v", id, got)
		}
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{"job_id": "delivered-job"})
	if rpcErr == nil || rpcErr.Code != -32001 || rpcErr.Data["id"] != "webhook.job_delivered" {
		t.Fatalf("retry of a delivered job = %+v; want -32001 webhook.job_delivered", rpcErr)
	}
	if values, _ := rpcErr.Data["values"].(map[string]string); values["status"] != "delivered" {
		t.Errorf("values = %v; want status delivered", rpcErr.Data["values"])
	}
	_, rpcErr = callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{"job_id": "no-such-job"})
	if rpcErr == nil || rpcErr.Code != -32000 || rpcErr.Data["id"] != "webhook.job_not_found" {
		t.Errorf("retry of an unknown id = %+v; want -32000 webhook.job_not_found", rpcErr)
	}
}

// TestWebhookTools_CreateWebhook_stringSliceEvents verifies that events passed
// as []string (not []any) are accepted.
func TestWebhookTools_CreateWebhook_stringSliceEvents(t *testing.T) {
	srv, _ := newWebhookServer(t)
	// inject []string directly into the args map, bypassing JSON
	args := map[string]any{
		"url":    "https://example.com/hook3",
		"events": []string{"post.published", "post.created"},
	}
	res, rpcErr := srv.handleWebhookTool(newAdminCtx(), "create_webhook", args)
	if rpcErr != nil {
		t.Fatalf("create_webhook ([]string events): %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["id"] == nil {
		t.Error("create_webhook returned nil id")
	}
}
