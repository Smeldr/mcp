package mcp

import (
	"regexp"
	"testing"
)

// core T1222: no tool or argument description names a record of Smeldr's own
// instance. An agent reads them in its tool list, and on its instance an
// address such as D142 is another record or none. Examples use a one-digit
// number (D1, S1), which is a shape and not a record.

// descriptionAddress matches the addresses a description used to cite:
// D12, T884, A398, G12, S3893, P401.
var descriptionAddress = regexp.MustCompile(`\b[DTAGSP][0-9]{2,4}\b`)

func TestToolDescriptionsNameNoRecordAddresses(t *testing.T) {
	srv, _, _ := sinceServer(t)
	defs := map[string]mcpTool{}
	for name, d := range srv.toolDefsByName {
		defs[name] = d
	}
	// The tools a server lists only with a feature wired: read their
	// definitions directly, so the guard does not depend on the fixture.
	for _, list := range [][]mcpTool{
		addressToolDefs(), changeCheckToolDefs(), checkToolDefs(), compositionToolDefs(),
		decisionSupersedeToolDefs(), delegateToolDefs(), dynamicContentToolDefs(), grantToolDefs(),
		lookupTokenNamesToolDefs(), navToolDefs(true), nodeToolDefs(), orchestrationToolDefs(),
		pageMetaToolDefs(), previewToolDefs(), provenanceToolDefs(), reachabilityToolDefs(),
		redirectToolDefs(), relationToolDefs(), restCountToolDefs(), schemaToolDefs(), signalToolDefs(),
		standingToolDefs(), stateToolDefs(), stewardshipToolDefs(), subscriptionToolDefs(),
		sweepRunToolDefs(), tokenToolDefs(), uploadToolDefs(), webhookToolDefs(),
		{administratorsToolDef(), approversOfToolDef(), checkCoverageToolDef(), decisionsForPathsToolDef(),
			discoverToolDef(), listFindingsToolDef(), listRelationsToolDef(), searchToolDef()},
	} {
		for _, d := range list {
			defs[d.Name] = d
		}
	}
	// A field description that comes from a core module's MCPSchema is
	// core's text, guarded by core's own TestTextsNameNoRecordAddresses; it
	// follows the pinned core, so it is not read here.
	coreText := map[string]bool{}
	for _, m := range srv.modules {
		for _, f := range m.MCPSchema() {
			coreText[f.Description] = true
		}
	}
	checked := 0
	var walk func(tool, where string, v any)
	walk = func(tool, where string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if s, ok := e.(string); ok && k == "description" {
					if coreText[s] {
						continue
					}
					checked++
					if a := descriptionAddress.FindString(s); a != "" {
						t.Errorf("%s %s: the description names %s: %q", tool, where, a, s)
					}
					continue
				}
				walk(tool, where+"."+k, e)
			}
		case []any:
			for _, e := range x {
				walk(tool, where, e)
			}
		}
	}
	for name, d := range defs {
		checked++
		if a := descriptionAddress.FindString(d.Description); a != "" {
			t.Errorf("%s: the description names %s: %q", name, a, d.Description)
		}
		walk(name, "input", d.InputSchema)
	}
	if len(defs) < 100 || checked < 500 {
		t.Fatalf("read %d tools and %d descriptions; the guard is not reading the tool list", len(defs), checked)
	}
}
