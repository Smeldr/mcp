package mcp

import (
	"context"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestUpdateTool_SchemaOmitsStatusAndSlug: update_task does not offer status
// or a slug field (the "slug" property is the identifier); create_task still
// offers status.
func TestUpdateTool_SchemaOmitsStatusAndSlug(t *testing.T) {
	srv, _, _ := sinceServer(t)
	props := func(name string) map[string]any {
		tool, ok := srv.toolDefsByName[name]
		if !ok {
			t.Fatalf("no tool %s", name)
		}
		return tool.InputSchema["properties"].(map[string]any)
	}
	up := props("update_task")
	if _, ok := up["status"]; ok {
		t.Error("update_task offers status")
	}
	if slug, ok := up["slug"].(map[string]any); !ok || !strings.Contains(slug["description"].(string), "Item ID or slug") {
		t.Errorf("update_task slug = %v; want the identifier", up["slug"])
	}
	if _, ok := props("create_task")["status"]; !ok {
		t.Error("create_task lost status")
	}
}

// TestUpdateTool_StatusRefusedHumanIDAccepted: update_task with a status is
// refused with the message; an update addressed by the Task's human ID
// succeeds, because the identifier is not a field.
func TestUpdateTool_StatusRefusedHumanIDAccepted(t *testing.T) {
	srv, db, admin := sinceServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := repo.Save(context.Background(), &smeldr.Task{Node: smeldr.Node{ID: "t-u", Slug: "t-u-slug", Status: "backlog"}, TaskID: "T99", Band: "core"}); err != nil {
		t.Fatal(err)
	}
	_, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"slug": "t-u-slug", "status": "resolved"})
	if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "transition_item") {
		t.Fatalf("status update = %+v; want -32602 naming transition_item", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"slug": "T99", "band": "cloud"}); rpcErr != nil {
		t.Fatalf("update by human ID: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"slug": "t-u-slug", "band": "site"}); rpcErr != nil {
		t.Fatalf("update by slug: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"slug": "t-u-slug", "Status": "done"}); rpcErr == nil || !strings.Contains(rpcErr.Message, "transition_item") {
		t.Errorf("Status (capitalised) = %+v; want the transition_item message", rpcErr)
	}
	stored, err := repo.FindByID(context.Background(), "t-u")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Band != "site" || stored.Status != "backlog" {
		t.Errorf("stored band %q status %q; want site, backlog", stored.Band, stored.Status)
	}
}

// TestUpdateFieldsOf drops only the identifier.
func TestUpdateFieldsOf(t *testing.T) {
	got := updateFieldsOf(map[string]any{"id": "x", "slug": "y", "title": "t", "status": "s"})
	if len(got) != 2 || got["title"] != "t" || got["status"] != "s" {
		t.Errorf("fields = %v", got)
	}
}
