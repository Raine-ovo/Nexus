package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rainea/nexus/pkg/types"
)

// SkillProvider abstracts the intelligence.SkillManager so this package avoids
// importing internal/intelligence (which would create a dependency cycle).
type SkillProvider interface {
	LoadSkill(name string) (string, error)
	GetIndexPrompt() string
}

// SkillLister returns skill names and descriptions for the list_skills tool.
// Each element is [name, description].
type SkillLister interface {
	ListSkillSummaries() [][2]string
}

// RegisterSkillTools registers load_skill and list_skills tools.
// provider supplies the on-demand loading and listing capabilities.
func RegisterSkillTools(reg RegisterFunc, provider SkillProvider, lister SkillLister) error {
	tools := []*types.ToolMeta{
		newLoadSkillTool(provider),
		newListSkillsTool(lister),
	}
	for _, t := range tools {
		if err := reg(t); err != nil {
			return err
		}
	}
	return nil
}

func newLoadSkillTool(provider SkillProvider) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "load_skill",
			Description: "Load the full content of a named skill. Skills are domain-specific knowledge modules (e.g. go-review, security-audit, api-design). Use list_skills first to discover available skill names.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Skill name as shown in list_skills output",
					},
				},
				"required": []string{"name"},
			},
		},
		Permission: types.PermRead,
		Source:     "builtin/skill",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			name := strings.TrimSpace(fmt.Sprintf("%v", args["name"]))
			if name == "" {
				return &types.ToolResult{Content: "skill name is required", IsError: true}, nil
			}
			if provider == nil {
				return &types.ToolResult{Content: "skill provider not configured", IsError: true}, nil
			}
			body, err := provider.LoadSkill(name)
			if err != nil {
				return &types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			out := map[string]interface{}{
				"skill":   name,
				"content": body,
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return &types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return &types.ToolResult{Content: string(b), IsError: false}, nil
		},
	}
}

func newListSkillsTool(lister SkillLister) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "list_skills",
			Description: "List all available skills with names and descriptions. Use load_skill with a name to get the full skill content.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Permission: types.PermRead,
		Source:     "builtin/skill",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			_ = args
			if lister == nil {
				return &types.ToolResult{Content: "skill lister not configured", IsError: true}, nil
			}
			summaries := lister.ListSkillSummaries()
			type entry struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			entries := make([]entry, 0, len(summaries))
			for _, s := range summaries {
				entries = append(entries, entry{Name: s[0], Description: s[1]})
			}
			out := map[string]interface{}{
				"skills": entries,
				"count":  len(entries),
				"hint":   "Call load_skill with a skill name to get the full knowledge content.",
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return &types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return &types.ToolResult{Content: string(b), IsError: false}, nil
		},
	}
}
