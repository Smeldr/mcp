package mcp

import "smeldr.dev/core"

// roleFor returns the minimum [smeldr.Role] required for the given dynamic
// content tool name. T49 will replace this with a DB-driven lookup without
// call-site changes.
func roleFor(toolName string) smeldr.Role {
	switch toolName {
	case "define_content_type":
		return smeldr.Admin
	case "create_content", "update_content", "set_content_status":
		return smeldr.Editor
	case "get_content", "list_content":
		return smeldr.Author
	default:
		return smeldr.Admin
	}
}
