package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"smeldr.dev/core"
	"smeldr.dev/oauth"
)

// plainStore hides every method but oauth.Store, so the oauth server sees a
// store without Dynamic Client Registration.
type plainStore struct{ oauth.Store }

// registeredApp wires an oauth server over store into an App through Register
// and returns the App's handler.
func registeredApp(t *testing.T, store oauth.Store, before func(*smeldr.App)) http.Handler {
	t.Helper()
	app := newTestApp(t, smeldr.MCP(smeldr.MCPRead))
	if before != nil {
		before(app)
	}
	oauthSrv := oauth.New(oauth.Config{
		Issuer:       "https://cms.example.com",
		Resource:     "https://cms.example.com/mcp",
		VerifyBearer: func(string) bool { return true },
	}, store)
	New(app, WithOAuth(oauthSrv)).Register(app)
	return app.Handler()
}

func discovery(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discovery status = %d", w.Code)
	}
	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("discovery JSON: %v", err)
	}
	return meta
}

func sqliteStore(t *testing.T) *oauth.SQLiteStore {
	t.Helper()
	s, err := oauth.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// With a RegistrationStore, discovery advertises registration and the App
// serves it: a valid RFC 7591 registration returns 201 with a dcr_ client_id.
func TestRegister_OAuthRegistrationMounted(t *testing.T) {
	h := registeredApp(t, sqliteStore(t), nil)
	meta := discovery(t, h)
	endpoint, _ := meta["registration_endpoint"].(string)
	if endpoint == "" {
		t.Fatalf("discovery has no registration_endpoint: %v", meta)
	}
	u, _ := url.Parse(endpoint)
	body := `{"redirect_uris":["https://client.example/cb"],"client_name":"test client"}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, u.Path, strings.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("POST %s = %d %s, want 201", u.Path, w.Code, w.Body.String())
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reg); err != nil || !strings.HasPrefix(reg.ClientID, "dcr_") {
		t.Errorf("registration = %s (%v), want a dcr_ client_id", w.Body.String(), err)
	}
}

// Without a RegistrationStore, neither the discovery entry nor the route exists.
func TestRegister_OAuthRegistrationAbsentWithoutStore(t *testing.T) {
	h := registeredApp(t, plainStore{sqliteStore(t)}, nil)
	if v, ok := discovery(t, h)["registration_endpoint"]; ok {
		t.Errorf("registration_endpoint = %v, want absent", v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{}`)))
	if w.Code != http.StatusNotFound {
		t.Errorf("POST /oauth/register = %d, want 404", w.Code)
	}
}

// The drift guard: every endpoint the discovery document advertises is served
// by the App for its method. It fails if oauth advertises a route Register does
// not reach, whichever side changed.
func TestRegister_DiscoveryEndpointsAreServed(t *testing.T) {
	for name, store := range map[string]oauth.Store{"with registration": sqliteStore(t), "without": plainStore{sqliteStore(t)}} {
		h := registeredApp(t, store, nil)
		methods := map[string]string{"authorization_endpoint": http.MethodGet, "token_endpoint": http.MethodPost,
			"registration_endpoint": http.MethodPost, "revocation_endpoint": http.MethodPost}
		meta := discovery(t, h)
		seen := 0
		for key, v := range meta {
			if !strings.HasSuffix(key, "_endpoint") {
				continue
			}
			method, known := methods[key]
			if !known {
				t.Errorf("%s: discovery advertises %s, which this test does not know: add it with its method", name, key)
				continue
			}
			u, _ := url.Parse(v.(string))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, u.Path, strings.NewReader("")))
			if w.Code == http.StatusNotFound {
				t.Errorf("%s: %s %s (%s) is advertised but the App answers 404", name, method, u.Path, key)
			}
			seen++
		}
		if seen < 2 {
			t.Errorf("%s: only %d endpoints advertised", name, seen)
		}
	}
}

// An App route under /oauth/ keeps its handler, and the routes oauth serves
// without advertising them (revoke) are reached too.
func TestRegister_OAuthRoutesKeepAppRoutes(t *testing.T) {
	h := registeredApp(t, sqliteStore(t), func(app *smeldr.App) {
		app.Handle("GET /oauth/callback", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/oauth/callback", nil))
	if w.Code != http.StatusTeapot {
		t.Errorf("GET /oauth/callback = %d, want the App's own 418", w.Code)
	}
	for _, rt := range []struct{ method, path string }{{http.MethodPost, "/oauth/revoke"}, {http.MethodGet, "/oauth/authorize"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, strings.NewReader("")))
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s = 404, want it served by oauth", rt.method, rt.path)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/oauth/token", nil))
	if w.Code < 400 {
		t.Errorf("DELETE /oauth/token = %d, want it refused", w.Code)
	}
}

// Why Register's oauth patterns each carry a method and a full path: a conflict
// matrix against Go's ServeMux, both registration orders (mcp-register-oauth-
// routes-no-conflict, 2026-10-11). Do not "simplify" oauthRoutes to a prefix.
//
//	Our patterns                          Conflicts with App patterns
//	/oauth/ (mcp v1.59.0)                 GET /, POST /, DELETE /, GET /{path...}, GET /{a}/{b}
//	GET /oauth/ + POST /oauth/            /oauth/x, GET /{a}/{b}
//	method + full path (oauthRoutes)      none
//
// The matrix also covered "/" and "GET /{slug}" (no conflict with oauthRoutes);
// they are left out below only because smeldr.App.Handler registers those two
// patterns itself, so an App cannot add them.
//
// Each App catch-all below is registered before and after Register: no panic,
// the App's own handler still answers, and registration still reaches oauth.
func TestRegister_NoConflictWithAppCatchAlls(t *testing.T) {
	appPatterns := []struct{ pattern, method, path string }{
		{"GET /", http.MethodGet, "/"},
		{"POST /", http.MethodPost, "/"},
		{"DELETE /", http.MethodDelete, "/"},
		{"GET /{path...}", http.MethodGet, "/a/b/c"},
		{"GET /{a}/{b}", http.MethodGet, "/x/y"},
		{"/oauth/x", http.MethodGet, "/oauth/x"},
	}
	teapot := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	for _, ap := range appPatterns {
		for _, before := range []bool{true, false} {
			name := ap.pattern + map[bool]string{true: " before", false: " after"}[before]
			t.Run(name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Register panicked next to App pattern %q: %v", ap.pattern, r)
					}
				}()
				app := newTestApp(t, smeldr.MCP(smeldr.MCPRead))
				if before {
					app.Handle(ap.pattern, teapot)
				}
				oauthSrv := oauth.New(oauth.Config{
					Issuer:       "https://cms.example.com",
					Resource:     "https://cms.example.com/mcp",
					VerifyBearer: func(string) bool { return true },
				}, sqliteStore(t))
				New(app, WithOAuth(oauthSrv)).Register(app)
				if !before {
					app.Handle(ap.pattern, teapot)
				}
				h := app.Handler()

				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(ap.method, ap.path, nil))
				if w.Code != http.StatusTeapot {
					t.Errorf("%s %s = %d, want the App's own 418", ap.method, ap.path, w.Code)
				}
				w = httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/oauth/register",
					strings.NewReader(`{"redirect_uris":["https://client.example/cb"]}`)))
				if w.Code != http.StatusCreated {
					t.Errorf("POST /oauth/register = %d %s, want 201", w.Code, w.Body.String())
				}
			})
		}
	}
}
