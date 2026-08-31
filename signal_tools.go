// AGPL-3.0-or-later

package mcp

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"smeldr.dev/core"
)

// signalToolDefs returns the two tool definitions for signal protocol management.
// Both are registered when App.Config().DB is non-nil (same guard used in
// handleToolsList and handleToolsCall).
//
// Role assignment:
//   - create_signal — Author (pilots create signals)
//   - list_signals  — Author (read-only signal query)
func signalToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "create_signal",
			Description: "Create a protocol signal in the smeldr_signals table with " +
				"status 'pending'. Used to record pilot-to-architect or " +
				"architect-to-pilot hand-offs. Requires the smeldr_signals table " +
				"created by smeldr.CreateOrchestrationTables. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sender":      map[string]any{"type": "string", "description": "Originating agent identifier, e.g. \"core\", \"architect\"."},
					"receiver":    map[string]any{"type": "string", "description": "Destination agent identifier."},
					"signal_type": map[string]any{"type": "string", "description": "Protocol verb, e.g. \"plan-ready\", \"commit-ready\"."},
					"task_ref":    map[string]any{"type": "string", "description": "Task or amendment identifier this signal relates to, e.g. \"T23\", \"A185\"."},
					"message":     map[string]any{"type": "string", "description": "Free-text body of the signal."},
					"sequence":    map[string]any{"type": "integer", "description": "Per-task monotonic counter that orders signals in a conversation."},
				},
				"required": []string{"sender", "receiver", "signal_type"},
			},
		},
		{
			Name: "list_signals",
			Description: "List protocol signals from smeldr_signals filtered by receiver " +
				"and status. Returns signals ordered by created_at ascending. " +
				"Returns an empty list when the smeldr_signals table does not exist " +
				"(fail-open). Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"receiver": map[string]any{"type": "string", "description": "Filter by destination agent identifier."},
					"state":    map[string]any{"type": "string", "description": "Filter by status (default \"pending\")."},
				},
				"required": []string{"receiver"},
			},
		},
	}
}

// isSignalTool reports whether name is one of the two signal protocol tools.
func isSignalTool(name string) bool {
	switch name {
	case "create_signal", "list_signals":
		return true
	}
	return false
}

// handleSignalTool dispatches the two signal protocol tools. Called only when
// s.app.Config().DB is non-nil and the caller holds Author role (checked by
// the caller in handleToolsCall).
func (s *Server) handleSignalTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	db := s.app.Config().DB

	switch name {
	case "create_signal":
		sender, ok := stringArg(args, "sender")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: sender required"}
		}
		receiver, ok := stringArg(args, "receiver")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: receiver required"}
		}
		signalType, ok := stringArg(args, "signal_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: signal_type required"}
		}
		taskRef := stringArgOr(args, "task_ref", "")
		message := stringArgOr(args, "message", "")
		sequence := intArgOr(args, "sequence", 0)

		id := smeldr.NewID()
		slug := signalSlug(sender, signalType, id)
		now := time.Now().UTC()

		_, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_signals
				(id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence)
			VALUES
				(?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, slug, now, now, sender, receiver, signalType, message, taskRef, sequence,
		)
		if err != nil {
			slog.ErrorContext(ctx, "mcp: create_signal: db exec failed", "error", err)
			return nil, &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
		}
		// This raw INSERT bypasses Module[Signal].MCPCreate entirely, so no
		// lifecycle hook fires on its own — App.NotifySignalCreated (core
		// v1.74.0+) restores the webhook/event-stream notification a
		// generic Module[T] create would have produced automatically.
		s.app.NotifySignalCreated(ctx, id, slug)
		return toolResult(map[string]any{
			"id":     id,
			"slug":   slug,
			"status": "pending",
		}), nil

	case "list_signals":
		receiver, ok := stringArg(args, "receiver")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: receiver required"}
		}
		state := stringArgOr(args, "state", "pending")

		rows, err := db.QueryContext(ctx,
			`SELECT id, slug, status, created_at, updated_at, sender, receiver,
			        signal_type, message, task_ref, sequence,
			        subject_type, subject_id, from_state, to_state, required_role
			FROM smeldr_signals
			WHERE receiver = ? AND status = ?
			ORDER BY created_at ASC`,
			receiver, state,
		)
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				slog.WarnContext(ctx, "mcp: list_signals: smeldr_signals table not found — returning empty list")
				return toolResult(map[string]any{"signals": []map[string]any{}, "count": 0}), nil
			}
			return nil, &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
		}
		defer rows.Close()

		var signals []map[string]any
		for rows.Next() {
			var (
				id, slug, status, sender, recv, sigType, message, taskRef string
				createdAt, updatedAt                                      string
				sequence                                                  int
				subjectType, subjectID, fromState, toState, requiredRole  string
			)
			if err := rows.Scan(&id, &slug, &status, &createdAt, &updatedAt,
				&sender, &recv, &sigType, &message, &taskRef, &sequence,
				&subjectType, &subjectID, &fromState, &toState, &requiredRole); err != nil {
				return nil, &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
			}
			m := map[string]any{
				"id":          id,
				"slug":        slug,
				"status":      status,
				"created_at":  createdAt,
				"updated_at":  updatedAt,
				"sender":      sender,
				"receiver":    recv,
				"signal_type": sigType,
				"message":     message,
				"task_ref":    taskRef,
				"sequence":    sequence,
			}
			// subject_type/subject_id/from_state/to_state/required_role (A296)
			// are always written together by recordAuthorizationRequiredSignal
			// (core, state.go) for a system-generated Signal, and always empty
			// together for an ordinary human-authored one — gated as one group
			// on subject_type, not five independent checks.
			if subjectType != "" {
				m["subject_type"] = subjectType
				m["subject_id"] = subjectID
				m["from_state"] = fromState
				m["to_state"] = toState
				m["required_role"] = requiredRole
			}
			signals = append(signals, m)
		}
		if err := rows.Err(); err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
		}
		if signals == nil {
			signals = []map[string]any{}
		}
		return toolResult(map[string]any{"signals": signals, "count": len(signals)}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown signal tool: " + name}
}

// signalSlug derives a human-readable unique slug for a signal record from
// the sender, signal type, and a UUID fragment. Example output:
// "core-plan-ready-01936b4f".
func signalSlug(sender, signalType, id string) string {
	base := smeldr.GenerateSlug(fmt.Sprintf("%s-%s", sender, signalType))
	suffix := id
	if len(suffix) > 8 {
		// The tail, not the head: smeldr.NewID() is a UUIDv7
		// ("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10],
		// b[10:16]) — id's first 8 characters (b[0:4]) are the top 32 bits
		// of a 48-bit millisecond clock, zero random bits, identical for
		// any two calls within the same ~65.5s window. The last 8
		// characters fall entirely within b[10:16], filled by
		// crypto/rand — genuinely random, no time-correlation.
		suffix = suffix[len(suffix)-8:]
	}
	return base + "-" + suffix
}
