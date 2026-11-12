package intercept

// OperatorDecidedTool reports whether the tool asks a person about every call
// where it runs, whatever the rules say: the tools that define what agents run
// later. The gate applies only an explicit denial to them, so that the one
// decision is neither asked for nor used up twice.
func OperatorDecidedTool(tool string) bool {
	switch tool {
	case "create_skill", "update_skill_file", "create_custom_tool", "update_custom_tool", "create_mcp", "update_mcp":
		return true
	}
	return false
}
