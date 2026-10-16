package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"smeldr.dev/core"
)

// sessionOwner is the token an SSE session belongs to: the one that first
// subscribed it. Its fingerprint is the token's stored ID (the sha256 of the
// bearer), the ID a revocation names.
type sessionOwner struct {
	userID      string
	roles       []smeldr.Role
	fingerprint string
}

// notifyVerdict is what a session's owner check says about one notification.
type notifyVerdict int

const (
	notifyDeliver notifyVerdict = iota // the owner may read the item
	notifySkip                         // not readable, or the check failed: skip this one
	notifyDrop                         // the owner's token is revoked: remove the session
)

// subscriptionRegistry tracks which URIs each SSE connection has subscribed to,
// the token each connection belongs to, and holds a per-connection send
// function for pushing resource-update notifications back over the SSE stream.
//
// The registry is safe for concurrent access. All exported methods acquire the
// appropriate lock before modifying state.
type subscriptionRegistry struct {
	mu    sync.RWMutex
	subs  map[string]map[string]struct{} // connID → set of subscribed URIs
	send  map[string]func(uri string)    // connID → SSE send function
	owner map[string]sessionOwner        // connID → the token it belongs to

	// check decides, per bound session, whether a notification about an
	// item reaches it (set by New: revocation, then the read rules).
	check func(owner sessionOwner, typeName, id string) notifyVerdict
}

// newSubscriptionRegistry returns an initialised registry ready for use.
func newSubscriptionRegistry() *subscriptionRegistry {
	return &subscriptionRegistry{
		subs:  make(map[string]map[string]struct{}),
		send:  make(map[string]func(uri string)),
		owner: make(map[string]sessionOwner),
	}
}

// Bind gives connID to o when it has no owner yet, and reports whether
// connID belongs to o's user: false when another user already holds it.
func (r *subscriptionRegistry) Bind(connID string, o sessionOwner) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, bound := r.owner[connID]
	if !bound {
		r.owner[connID] = o
		return true
	}
	return cur.userID == o.userID
}

// OwnedBy reports whether connID is unbound or belongs to userID.
func (r *subscriptionRegistry) OwnedBy(connID, userID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cur, bound := r.owner[connID]
	return !bound || cur.userID == userID
}

// Register adds uri to the subscription set for connID.
// Calling Register with a connID that has no prior entries creates the entry.
func (r *subscriptionRegistry) Register(connID, uri string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[connID]; !ok {
		r.subs[connID] = make(map[string]struct{})
	}
	r.subs[connID][uri] = struct{}{}
}

// Unsubscribe removes uri from connID's subscription set.
// It is a no-op when connID or uri is not registered.
func (r *subscriptionRegistry) Unsubscribe(connID, uri string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.subs[connID]; ok {
		delete(m, uri)
	}
}

// RemoveConn removes all subscriptions, the owner and the send function for
// connID. Called when an SSE connection closes or its token is revoked.
func (r *subscriptionRegistry) RemoveConn(connID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, connID)
	delete(r.send, connID)
	delete(r.owner, connID)
}

// RegisterSend stores fn as the notification sender for connID.
// fn is called by [Notify] with the URI of the updated resource.
func (r *subscriptionRegistry) RegisterSend(connID string, fn func(uri string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.send[connID] = fn
}

// Notify calls the send function for every connection that has subscribed to
// uri, with no owner check. It recovers from any panic in a send function to
// avoid crashing the caller. Item notifications go through [NotifyItem].
func (r *subscriptionRegistry) Notify(uri string) {
	r.notify(uri, "", "", false)
}

// NotifyItem is [Notify] for a change to the item typeName id: a session is
// sent the notification only when its owner may read the item and its token
// is not revoked; a revoked session is removed. With a check set, a session
// with no owner gets nothing (fail closed).
func (r *subscriptionRegistry) NotifyItem(uri, typeName, id string) {
	r.notify(uri, typeName, id, true)
}

func (r *subscriptionRegistry) notify(uri, typeName, id string, checked bool) {
	type target struct {
		connID string
		owner  sessionOwner
		bound  bool
		fn     func(string)
	}
	r.mu.RLock()
	var targets []target
	for connID, uris := range r.subs {
		if _, ok := uris[uri]; ok {
			if fn, ok := r.send[connID]; ok {
				o, bound := r.owner[connID]
				targets = append(targets, target{connID, o, bound, fn})
			}
		}
	}
	check := r.check
	r.mu.RUnlock()

	for _, t := range targets {
		if checked && check != nil {
			if !t.bound {
				continue
			}
			switch check(t.owner, typeName, id) {
			case notifySkip:
				continue
			case notifyDrop:
				r.RemoveConn(t.connID)
				continue
			}
		}
		func() {
			defer func() { recover() }() //nolint:errcheck
			t.fn(uri)
		}()
	}
}

// buildNotifyEvent formats an SSE event for a notifications/resources/updated
// notification. The payload follows the MCP protocol: {"uri": "<resource-uri>"}.
func buildNotifyEvent(uri string) string {
	payload, _ := json.Marshal(map[string]any{"uri": uri})
	return fmt.Sprintf("event: notifications/resources/updated\ndata: %s\n\n", payload)
}

// tokenFingerprint is the stored ID of a bearer token (its sha256, hex), as
// core's TokenStore keeps it; empty for an empty token.
func tokenFingerprint(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newSessionID returns a cryptographically random 16-byte hex string suitable
// for use as an SSE connection identifier in resource subscription requests.
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-based ID if rand is unavailable.
		return fmt.Sprintf("%016x", uint64(0))
	}
	return hex.EncodeToString(b)
}
