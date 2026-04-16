package permission

// defaultAllowToolNames is a conservative read-only baseline for semi_auto mode.
// These tools stay inside the workspace or only expose already persisted state.
var defaultAllowToolNames = []string{
	"read_file",
	"list_dir",
	"grep_search",
	"glob_search",
	"load_skill",
	"list_skills",
	"read_inbox",
	"list_teammates",
	"list_pending_requests",
	"search_knowledge",
	"list_knowledge_bases",
	"review_file",
	"check_patterns",
	"list_tasks",
	"get_task",
	"monitor_progress",
}

func registerDefaultRules(r *RuleEngine) {
	if r == nil {
		return
	}
	r.AddRule(Rule{
		ID:          "builtin-safe-read",
		Type:        "allow",
		ToolNames:   append([]string(nil), defaultAllowToolNames...),
		Description: "Allow common read-only tools by default in semi_auto mode",
	})
}
