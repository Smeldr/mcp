// AGPL-3.0-or-later

package mcp

import (
	"smeldr.dev/core"
)

// createAddressNote ends the description of every create tool for a record
// type (D110 7.1): the caller learns in advance that the address is the
// server's, not theirs. A web-content type (one with a URL prefix) has no
// address and does not get it.
const createAddressNote = "The address (for example D110) is assigned by the server and returned in the result. " +
	"Do not send one. To record your own numbering, use external_ref."

// addressedCompiledTypes are the compiled types whose items get an address:
// core's six orchestration types (RegisterOrchestrationTypes), since core
// v1.156.0 its two authority types (RegisterAuthorityTypes), and since core
// T882 the four places of D142 (Organization, Domain, Area, Set). Every other
// compiled module is web content and has none.
var addressedCompiledTypes = map[string]bool{
	"Decision": true, "Amendment": true, "Task": true, "Goal": true, "Signal": true, "Run": true,
	"Rule": true, "AuthorityStub": true,
	"Organization": true, "Domain": true, "Area": true, "Set": true,
}

// createDescription is the description of a generated create_{type} tool,
// with [createAddressNote] for a type whose items get an address.
func createDescription(typeName string) string {
	d := "Create a new " + typeName + " content item."
	if addressedCompiledTypes[typeName] {
		d += " " + createAddressNote
	}
	return d
}

// identifierDescription is the description of every argument that names one
// item (D110 7.1): core resolves an address, a slug or an ID, in that order.
const identifierDescription = "ID, slug or address, all accepted."

// addressPrefixProp is the address_prefix property of define_content_type and
// redefine_content_type; empty names what an omitted value does on that tool.
func addressPrefixProp(empty string) map[string]any {
	return map[string]any{
		"type": "string",
		"description": "The record address prefix (D110): 2 to 4 capital letters, for example \"INV\". " +
			empty + " A type with url_prefix is web content and has none. A prefix already used by another type is refused.",
	}
}

// addressToolDefs returns the migrate_addresses tool definition. Registered
// when App.Config().DB is non-nil.
//
// Role: Admin; under governance the "administer" operation (core's seeded
// smeldr_tool_policies row, A490). Instance infrastructure, like the token and
// webhook tools.
func addressToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "migrate_addresses",
			Description: "Give every record that has no address yet its address (D110), in one transaction. " +
				"Without apply (the default) this is a dry run: the same code runs and is rolled back, so the " +
				"report shows exactly what an apply would do and nothing is written. Applying is a governance " +
				"act: read a dry run first, and back up the database. The report lists, per type, the prefix, " +
				"counts before and after, every renumbered record with its old and new address, every rewritten " +
				"reference and every reference left alone, and duration_ms, how long the database writer was held. " +
				"Keep the returned report with the deploy record: renumbered Decisions are part of the governance " +
				"record, and the instance stores no copy. A second apply changes nothing. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"apply": map[string]any{
						"type":        "boolean",
						"description": "true applies the migration. Omit it, or send false, for a dry run.",
					},
				},
			},
		},
	}
}

// isAddressTool reports whether name is the migrate_addresses tool.
func isAddressTool(name string) bool {
	return name == "migrate_addresses"
}

// handleAddressTool runs migrate_addresses. Called only when
// s.app.Config().DB is non-nil and the caller is authorised (checked by the
// caller in handleToolsCall).
func (s *Server) handleAddressTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	apply := false
	if v, present := args["apply"]; present && v != nil {
		b, isBool := v.(bool)
		if !isBool {
			// Never guess: a non-boolean apply could be meant either way.
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: apply must be a boolean"}
		}
		apply = b
	}
	report, err := s.app.MigrateAddresses(ctx, smeldr.AddressMigrationOptions{DryRun: !apply})
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(report), nil
}
