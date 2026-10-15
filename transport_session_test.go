package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smeldr.dev/core"
)

// sessionFixture is an HTTP MCP server with one SSE session open: the
// session's message URL, a bearer token, and the stream's lines.
type sessionFixture struct {
	srv     *Server
	ts      *httptest.Server
	token   string
	message string
	lines   chan string
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	app, repo := newWriteApp(t)
	seedPost(t, repo, "watched", smeldr.Draft, "Watched Title", "watched body content here")
	srv := New(app)
	app.Handler() // wires the signal bus the listener in New rides on
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	tok, err := smeldr.SignToken(smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Admin}},
		"test-secret-32-bytes-xxxxxxxxxxxx", 0)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/mcp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	f := &sessionFixture{srv: srv, ts: ts, token: tok, lines: lines}
	for {
		line := f.next(t, 2*time.Second)
		if path, ok := strings.CutPrefix(line, "data: /mcp/message?session_id="); ok {
			f.message = ts.URL + "/mcp/message?session_id=" + path
			return f
		}
	}
}

// next is the stream's next line, failing the test after wait.
func (f *sessionFixture) next(t *testing.T, wait time.Duration) string {
	t.Helper()
	select {
	case line, ok := <-f.lines:
		if !ok {
			t.Fatal("SSE stream closed")
		}
		return line
	case <-time.After(wait):
		t.Fatal("timeout waiting for the SSE stream")
	}
	return ""
}

// call posts one JSON-RPC request to url and returns its result or error.
func (f *sessionFixture) call(t *testing.T, url, method string, params any) jsonRPCResponse {
	t.Helper()
	raw, _ := json.Marshal(params)
	body, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: method, Params: raw})
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("%s: %d %s", method, out.Error.Code, out.Error.Message)
	}
	return out
}

// publish publishes the watched post over MCP, which fires a lifecycle event.
func (f *sessionFixture) publish(t *testing.T) {
	t.Helper()
	f.call(t, f.ts.URL+"/mcp/message", "tools/call",
		map[string]any{"name": "publish_test_mcp_post", "arguments": map[string]any{"slug": "watched"}})
}

// subscribed reports whether the registry holds uri for any session.
func (f *sessionFixture) subscribed(uri string) bool {
	r := f.srv.subscriptions
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, set := range r.subs {
		if _, ok := set[uri]; ok {
			return true
		}
	}
	return false
}

const watchedURI = "smeldr://posts/watched"

// resources/subscribe over HTTP registers the session named by session_id,
// and the update arrives on that SSE stream (mcp-resources-subscribe-session-id).
func TestHTTP_SubscribeDeliversUpdate(t *testing.T) {
	f := newSessionFixture(t)
	f.call(t, f.message, "resources/subscribe", map[string]any{"uri": watchedURI})
	if !f.subscribed(watchedURI) {
		t.Fatal("resources/subscribe over HTTP registered nothing")
	}
	f.publish(t)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line := f.next(t, time.Until(deadline))
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, watchedURI) {
			return
		}
	}
	t.Fatal("no notifications/resources/updated for the subscribed URI")
}

// resources/unsubscribe over HTTP removes the session's subscription.
func TestHTTP_UnsubscribeStopsUpdates(t *testing.T) {
	f := newSessionFixture(t)
	f.call(t, f.message, "resources/subscribe", map[string]any{"uri": watchedURI})
	f.call(t, f.message, "resources/unsubscribe", map[string]any{"uri": watchedURI})
	if f.subscribed(watchedURI) {
		t.Fatal("resources/unsubscribe over HTTP left the subscription")
	}
	f.publish(t)
	select {
	case line := <-f.lines:
		if strings.Contains(line, watchedURI) {
			t.Errorf("notification after unsubscribe: %q", line)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// Without session_id there is no session to subscribe: {} and nothing stored.
func TestHTTP_SubscribeWithoutSessionIsNoop(t *testing.T) {
	f := newSessionFixture(t)
	out := f.call(t, f.ts.URL+"/mcp", "resources/subscribe", map[string]any{"uri": watchedURI})
	if b, _ := json.Marshal(out.Result); string(b) != "{}" {
		t.Errorf("result = %s, want {}", b)
	}
	if f.subscribed(watchedURI) {
		t.Error("a subscription was stored without a session")
	}
}

// The per-call context carries the request and its values, the user, and an
// attached read scope, but a cancelled request does not cancel the call.
func TestHTTP_RequestContext(t *testing.T) {
	type key struct{}
	base, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	r := httptest.NewRequest(http.MethodPost, "/mcp/message?session_id=s1", nil).WithContext(base)
	user := smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Author}}
	ctx := newRequestContext(r, user)
	cancel()

	if ctx.Request() != r || ctx.User().ID != "u1" {
		t.Errorf("Request %v, User %q; want the live request and u1", ctx.Request() == r, ctx.User().ID)
	}
	if ctx.Value(key{}) != "v" {
		t.Error("the request's values are not carried")
	}
	if ctx.Err() != nil || ctx.Done() != nil {
		t.Errorf("a cancelled request cancelled the call: Err %v", ctx.Err())
	}
	if _, ok := ctx.Deadline(); ok {
		t.Error("the call has a deadline")
	}
	scoped := smeldr.WithReadScope(ctx, smeldr.AllReadScope())
	if s, ok := smeldr.ReadScopeFrom(scoped); !ok || !s.All() {
		t.Error("an attached read scope is not found")
	}
	if c, ok := scoped.(smeldr.Context); !ok || c.User().ID != "u1" {
		t.Error("the scoped context lost the user")
	}
	if ctx.Response() == nil {
		t.Error("Response is nil")
	}
}
