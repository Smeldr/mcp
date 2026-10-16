package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// new_slug and ID-bound previews (mcp-update-content-slug; core A507 rules).

func TestNewSlug_CompiledWebContent(t *testing.T) {
	app, repo := newWriteApp(t)
	srv := New(app)
	seedPost(t, repo, "about", smeldr.Published, "About", "the published about page")
	second := seedPost(t, repo, "second", smeldr.Draft, "About Two", "a second about page draft")
	second.Slug = "about"
	if err := repo.Save(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	_, rpcErr := callTool(t, srv, newEditorCtx(), "publish_test_mcp_post", map[string]any{"slug": second.ID})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "for example /posts/about-2") {
		t.Fatalf("publish over a published URL = %+v; want core's guided conflict", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "update_test_mcp_post", map[string]any{"slug": second.ID, "new_slug": "about-2"}); rpcErr != nil {
		t.Fatalf("new_slug on a draft: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "publish_test_mcp_post", map[string]any{"slug": "about-2"}); rpcErr != nil {
		t.Fatalf("publish after new_slug: %v", rpcErr.Message)
	}
	// Published now: the URL stays.
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "update_test_mcp_post", map[string]any{"slug": "about-2", "new_slug": "about-3"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("new_slug on a published item = %+v; want -32602", rpcErr)
	}
	draft := seedPost(t, repo, "draft-x", smeldr.Draft, "Draft X", "another draft body text")
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "update_test_mcp_post", map[string]any{"slug": draft.ID, "new_slug": "Not A Slug"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("an invalid new_slug = %+v; want -32602", rpcErr)
	}
}

func TestNewSlug_Schemas(t *testing.T) {
	webSchema := updateSchemaFor("Post", nil)
	if _, ok := webSchema["properties"].(map[string]any)["new_slug"]; !ok {
		t.Error("a web-content update schema has no new_slug")
	}
	if !strings.Contains(updateDescription("Post"), "new_slug") {
		t.Error("a web-content update description does not name new_slug")
	}
	for _, record := range []string{"Task", "Rule", "AuthorityStub"} {
		if _, ok := updateSchemaFor(record, nil)["properties"].(map[string]any)["new_slug"]; ok {
			t.Errorf("%s's update schema offers new_slug", record)
		}
		if strings.Contains(updateDescription(record), "new_slug") {
			t.Errorf("%s's update description names new_slug", record)
		}
	}
	for _, record := range []string{"Rule", "AuthorityStub"} {
		if !strings.Contains(createDescription(record), createAddressNote) {
			t.Errorf("%s's create description lacks the address note", record)
		}
	}
}

func TestNewSlug_Dynamic(t *testing.T) {
	srv, _ := newDynamicServer(t)
	repo := defineDynamic(t, srv, "article", "/articles")
	// Two drafts share a slug (a draft reserves no URL), then one is published.
	a := dynamicItem(t, repo, "Pasta", false)
	b := dynamicItem(t, repo, "Pasta", false)
	if a.Slug != "pasta" || b.Slug != "pasta" {
		t.Fatalf("draft slugs = %q %q; want both pasta", a.Slug, b.Slug)
	}
	if err := repo.SetStatus(context.Background(), a.ID, smeldr.Published); err != nil {
		t.Fatal(err)
	}

	update := func(args map[string]any) (any, *jsonRPCError) {
		args["type_name"] = "article"
		return callTool(t, srv, newEditorCtx(), "update_content", args)
	}
	if _, rpcErr := update(map[string]any{"id": b.ID}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("neither fields nor new_slug = %+v; want -32602", rpcErr)
	}
	// A slug a published item holds is accepted at the change; publish refuses it.
	c := dynamicItem(t, repo, "Soup", false)
	if _, rpcErr := update(map[string]any{"id": c.ID, "new_slug": "pasta"}); rpcErr != nil {
		t.Fatalf("new_slug to a published slug: %v", rpcErr.Message)
	}
	_, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{"type_name": "article", "id": c.ID, "status": "published"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "/articles/pasta is already published") {
		t.Errorf("publish over a published slug = %+v; want the guided conflict", rpcErr)
	}

	res, rpcErr := update(map[string]any{"id": b.ID, "new_slug": "pasta-2"})
	if rpcErr != nil {
		t.Fatalf("new_slug only: %v", rpcErr.Message)
	}
	if !strings.Contains(toolText(res), `"Slug":"pasta-2"`) {
		t.Errorf("result = %s; want the new slug", toolText(res))
	}
	if _, rpcErr := update(map[string]any{"id": b.ID, "new_slug": "pasta-two", "fields": map[string]any{"title": "Pasta Two"}}); rpcErr != nil {
		t.Fatalf("new_slug with fields: %v", rpcErr.Message)
	}
	node, _ := repo.GetByID(context.Background(), b.ID)
	if node.Slug != "pasta-two" || !strings.Contains(string(node.Fields), "Pasta Two") {
		t.Errorf("stored = %q %s; want both changes", node.Slug, node.Fields)
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{"type_name": "article", "id": b.ID, "status": "published"}); rpcErr != nil {
		t.Fatalf("publish after new_slug: %v", rpcErr.Message)
	}
	if _, rpcErr := update(map[string]any{"id": b.ID, "new_slug": "pasta-three"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("new_slug on a published item = %+v; want -32001", rpcErr)
	}
	if _, rpcErr := update(map[string]any{"id": c.ID, "new_slug": "Bad Slug"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("an invalid new_slug = %+v; want -32602", rpcErr)
	}
	if _, rpcErr := update(map[string]any{"id": "no-such-item", "new_slug": "x"}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("a missing item = %+v; want not found", rpcErr)
	}
	if _, rpcErr := update(map[string]any{"id": c.ID, "new_slug": "soup", "fields": "not an object"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("fields not an object = %+v; want -32602", rpcErr)
	}

	// A record type keeps its slug.
	notes := defineDynamic(t, srv, "note", "")
	n := dynamicItem(t, notes, "Note", false)
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "update_content", map[string]any{"type_name": "note", "id": n.ID, "new_slug": "x"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("new_slug on a record type = %+v; want -32602", rpcErr)
	}
}

// One of two drafts sharing a slug, named by ID, gets a URL whose token opens
// exactly that draft; named by the shared slug, the answer asks for the ID.
func TestPreviewURL_BindsTheID(t *testing.T) {
	app, repo := newWriteApp(t)
	srv := New(app)
	a := seedPost(t, repo, "about", smeldr.Draft, "About A", "the first about draft")
	b := seedPost(t, repo, "about-b", smeldr.Draft, "About B", "the second about draft")
	b.Slug = "about"
	if err := repo.Save(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	h := app.Handler()

	if _, rpcErr := callTool(t, srv, newEditorCtx(), "create_preview_url", map[string]any{"prefix": "/posts", "slug": "about"}); rpcErr == nil || !strings.Contains(rpcErr.Message, "use its ID") {
		t.Fatalf("a shared slug = %+v; want use its ID", rpcErr)
	}
	for _, want := range []*testMCPPost{a, b} {
		res, rpcErr := callTool(t, srv, newEditorCtx(), "create_preview_url", map[string]any{"prefix": "/posts", "slug": want.ID})
		if rpcErr != nil {
			t.Fatalf("preview by ID: %v", rpcErr.Message)
		}
		var url string
		if err := json.Unmarshal([]byte(toolText(res)), &url); err != nil {
			t.Fatal(err)
		}
		path, ok := strings.CutPrefix(url, "http://localhost")
		if !ok || !strings.HasPrefix(path, "/posts/about?preview=") {
			t.Fatalf("url = %q; want the item's own slug", url)
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want.ID) {
			t.Errorf("GET %s = %d %s; want draft %s", path, w.Code, w.Body.String(), want.ID)
		}
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "create_preview_url", map[string]any{"prefix": "/posts", "slug": "no-such-post"}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("a missing item = %+v; want not found", rpcErr)
	}
	res, rpcErr := callTool(t, srv, newEditorCtx(), "create_preview_url", map[string]any{"prefix": "/no-module", "slug": "x"})
	if rpcErr != nil || !strings.Contains(toolText(res), "/no-module/x?preview=") {
		t.Errorf("a prefix with no module = %v, %+v; want a slug URL", res, rpcErr)
	}
}

func TestIDOf(t *testing.T) {
	var nilPost *testMCPPost
	for _, c := range []struct {
		item any
		want string
	}{
		{&testMCPPost{Node: smeldr.Node{ID: "x"}}, "x"},
		{nilPost, ""},
		{"not a struct", ""},
		{struct{ ID int }{1}, ""},
	} {
		if got := idOf(c.item); got != c.want {
			t.Errorf("idOf(%T) = %q; want %q", c.item, got, c.want)
		}
	}
}
