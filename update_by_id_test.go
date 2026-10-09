package mcp

import (
	"context"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestUpdateTool_ByIDAlias: update_task accepts "id" as the alias of "slug"
// (node ID or human ID), an unknown key and a status are still refused, and
// two different identifiers are refused on every identifier tool without
// touching the item.
func TestUpdateTool_ByIDAlias(t *testing.T) {
	srv, db, admin := sinceServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := repo.Save(context.Background(), &smeldr.Task{Node: smeldr.Node{ID: "t-id", Slug: "t-id-slug", Status: "backlog"}, TaskID: "T98", Band: "core"}); err != nil {
		t.Fatal(err)
	}
	band := func() string {
		stored, err := repo.FindByID(context.Background(), "t-id")
		if err != nil {
			t.Fatal(err)
		}
		return stored.Band
	}

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"id": "t-id", "band": "cloud"}, "cloud"},
		{map[string]any{"id": "T98", "band": "site"}, "site"},
		{map[string]any{"slug": "t-id", "band": "devops"}, "devops"},
		{map[string]any{"id": "t-id-slug", "slug": "t-id-slug", "band": "brand"}, "brand"},
	} {
		if _, rpcErr := callTool(t, srv, admin, "update_task", c.args); rpcErr != nil {
			t.Fatalf("update_task %v: %v", c.args, rpcErr.Message)
		}
		if got := band(); got != c.want {
			t.Errorf("after %v band = %q; want %q", c.args, got, c.want)
		}
	}

	if _, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"id": "t-id", "nope": "x"}); rpcErr == nil || !strings.Contains(rpcErr.Message, "unknown parameter: nope") {
		t.Errorf("unknown key = %+v; want refused", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"id": "t-id", "status": "done"}); rpcErr == nil || !strings.Contains(rpcErr.Message, "transition_item") {
		t.Errorf("status = %+v; want the transition_item message", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, admin, "update_task", map[string]any{"band": "x"}); rpcErr == nil || !strings.Contains(rpcErr.Message, "id (or slug) required") {
		t.Errorf("no identifier = %+v", rpcErr)
	}

	two := map[string]any{"id": "t-id", "slug": "someone-else"}
	for _, tool := range []string{"update_task", "get_task", "publish_task", "schedule_task", "archive_task", "delete_task"} {
		args := map[string]any{"scheduled_at": "2030-01-01T00:00:00Z"}
		for k, v := range two {
			args[k] = v
		}
		if tool == "update_task" {
			args = map[string]any{"id": "t-id", "slug": "someone-else", "band": "x"}
		}
		_, rpcErr := callTool(t, srv, admin, tool, args)
		if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "not two different identifiers") {
			t.Errorf("%s with two identifiers = %+v; want -32602", tool, rpcErr)
		}
	}
	stored, err := repo.FindByID(context.Background(), "t-id")
	if err != nil {
		t.Fatalf("item gone after refused calls: %v", err)
	}
	if stored.Band != "brand" || stored.Status != "backlog" {
		t.Errorf("stored band %q status %q; want brand, backlog (untouched)", stored.Band, stored.Status)
	}
}
