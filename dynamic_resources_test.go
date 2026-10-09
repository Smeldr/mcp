package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// defineDynamic registers a runtime-defined type, public when prefix is set,
// and returns its repository.
func defineDynamic(t *testing.T, srv *Server, typeName, prefix string) *smeldr.DynamicTypeRepo {
	t.Helper()
	fields, _ := json.Marshal([]smeldr.SchemaField{{Name: "title", Type: "string", Required: true, Role: "title"}})
	if _, err := srv.app.DefineContentType(context.Background(), &smeldr.ContentTypeSchema{TypeName: typeName, URLPrefix: prefix, Fields: fields}); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := srv.app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func dynamicItem(t *testing.T, repo *smeldr.DynamicTypeRepo, title string, publish bool) *smeldr.DynamicNode {
	t.Helper()
	node, err := repo.CreateDraft(context.Background(), map[string]any{"title": title})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if publish {
		if err := repo.SetStatus(context.Background(), node.ID, smeldr.Published); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
	}
	return node
}

// TestDynamicResources_ListTemplatesRead: a public runtime-defined type's
// published items are resources under smeldr:/<prefix>/<slug>; drafts and
// admin-only types are not.
func TestDynamicResources_ListTemplatesRead(t *testing.T) {
	srv, _ := newDynamicServer(t)
	recipes := defineDynamic(t, srv, "recipe", "/recipes")
	notes := defineDynamic(t, srv, "note", "")
	pub := dynamicItem(t, recipes, "Pancakes", true)
	draft := dynamicItem(t, recipes, "Waffles", false)
	hidden := dynamicItem(t, notes, "Secret", true)
	ctx := newAuthorCtx()

	var uris []string
	for _, r := range srv.allResources(ctx) {
		uris = append(uris, r.URI)
	}
	joined := strings.Join(uris, " ")
	if !strings.Contains(joined, "smeldr://recipes/"+pub.Slug) {
		t.Errorf("published recipe missing from %v", uris)
	}
	if strings.Contains(joined, draft.Slug) || strings.Contains(joined, hidden.Slug) {
		t.Errorf("draft or admin-only item listed: %v", uris)
	}

	tmpls := srv.handleResourcesTemplatesList().(map[string]any)["resourceTemplates"].([]resourceTemplate)
	var found, admin bool
	for _, tp := range tmpls {
		found = found || tp.URITemplate == "smeldr://recipes/{slug}"
		admin = admin || strings.Contains(tp.Name, "note")
	}
	if !found || admin {
		t.Errorf("templates = %+v; want recipes, not notes", tmpls)
	}

	read := func(uri string) (any, *jsonRPCError) {
		p, _ := json.Marshal(map[string]any{"uri": uri})
		return srv.handleResourcesRead(ctx, p)
	}
	res, rpcErr := read("smeldr://recipes/" + pub.Slug)
	if rpcErr != nil {
		t.Fatalf("read published: %v", rpcErr.Message)
	}
	if text := res.(map[string]any)["contents"].([]resourceContent)[0].Text; !strings.Contains(text, "Pancakes") {
		t.Errorf("content = %s", text)
	}
	for _, uri := range []string{"smeldr://recipes/" + draft.Slug, "smeldr://recipes/nope", "smeldr://recipes/a/b", "smeldr://notes/" + hidden.Slug} {
		if _, rpcErr := read(uri); rpcErr == nil || rpcErr.Code != -32001 {
			t.Errorf("read %s = %v; want not found", uri, rpcErr)
		}
	}
}

// TestDynamicResources_Notify: a subscriber to a public runtime-defined
// item is notified when it moves; an admin-only type's item notifies nobody.
func TestDynamicResources_Notify(t *testing.T) {
	srv, _ := newDynamicServer(t)
	recipes := defineDynamic(t, srv, "recipe", "/recipes")
	notes := defineDynamic(t, srv, "note", "")
	pub := dynamicItem(t, recipes, "Pancakes", false)
	note := dynamicItem(t, notes, "Secret", false)

	var mu sync.Mutex
	got := map[string]int{}
	srv.subscriptions.Register("c1", "smeldr://recipes/"+pub.Slug)
	srv.subscriptions.RegisterSend("c1", func(u string) { mu.Lock(); got[u]++; mu.Unlock() })
	if err := recipes.SetStatus(newEditorCtx(), pub.ID, smeldr.Published); err != nil {
		t.Fatal(err)
	}
	if err := notes.SetStatus(newEditorCtx(), note.ID, smeldr.Published); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := got["smeldr://recipes/"+pub.Slug]
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if got["smeldr://recipes/"+pub.Slug] == 0 {
		t.Errorf("no notification for the published recipe: %v", got)
	}
	for u := range got {
		if strings.Contains(u, note.Slug) {
			t.Errorf("admin-only item notified: %s", u)
		}
	}
}

// TestPublicDynamicTypes_NoRegistry: a server whose app has no runtime types
// lists none.
func TestPublicDynamicTypes_NoRegistry(t *testing.T) {
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx")})
	srv := New(app)
	if got := srv.publicDynamicTypes(); len(got) != 0 {
		t.Errorf("types = %v", got)
	}
	if _, _, ok := srv.dynamicTypeByPrefix("smeldr://x/y"); ok {
		t.Error("prefix matched with no types")
	}
}
