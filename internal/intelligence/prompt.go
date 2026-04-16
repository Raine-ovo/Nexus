package intelligence

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PromptAssembler combines bootstrap text, skill index, and memory section
// into the final system prompt with explicit token budgets.
type PromptAssembler struct {
	bootstrap    *BootstrapLoader
	skillManager *SkillManager
	budgets      PromptBudgets
}

// PromptBudgets caps each section and the assembled prompt (character budgets).
type PromptBudgets struct {
	BootstrapMax  int // default 150000 chars
	SkillIndexMax int // default 30000 chars
	MemoryMax     int // default 20000 chars
	TotalMax      int // default 200000 chars
}

func defaultBudgets() PromptBudgets {
	return PromptBudgets{
		BootstrapMax:  150000,
		SkillIndexMax: 30000,
		MemoryMax:     20000,
		TotalMax:      200000,
	}
}

// NewPromptAssembler wires bootstrap loading and skill indexing for workspaceDir.
// Skills are read from filepath.Join(workspaceDir, "skills").
func NewPromptAssembler(workspaceDir string) *PromptAssembler {
	skillsDir := filepath.Join(workspaceDir, "skills")
	bl := NewBootstrapLoader(workspaceDir)
	sm := NewSkillManager(skillsDir)
	return &PromptAssembler{
		bootstrap:    bl,
		skillManager: sm,
		budgets:      defaultBudgets(),
	}
}

// SkillManager returns the embedded skill manager for dependency injection.
func (a *PromptAssembler) SkillManager() *SkillManager {
	if a == nil {
		return nil
	}
	return a.skillManager
}

// BootstrapLoader returns the embedded bootstrap loader for tests or advanced use.
func (a *PromptAssembler) BootstrapLoader() *BootstrapLoader {
	if a == nil {
		return nil
	}
	return a.bootstrap
}

// SetBudgets replaces prompt section budgets. Zero values keep previous settings.
func (a *PromptAssembler) SetBudgets(b PromptBudgets) {
	if a == nil {
		return
	}
	if b.BootstrapMax > 0 {
		a.budgets.BootstrapMax = b.BootstrapMax
	}
	if b.SkillIndexMax > 0 {
		a.budgets.SkillIndexMax = b.SkillIndexMax
	}
	if b.MemoryMax > 0 {
		a.budgets.MemoryMax = b.MemoryMax
	}
	if b.TotalMax > 0 {
		a.budgets.TotalMax = b.TotalMax
	}
}

// Build assembles bootstrap text, skill index, and memorySection with per-section and total caps.
func (a *PromptAssembler) Build(memorySection string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("intelligence: nil PromptAssembler")
	}
	budgets := a.budgets
	if budgets.BootstrapMax <= 0 {
		budgets.BootstrapMax = defaultBudgets().BootstrapMax
	}
	if budgets.SkillIndexMax <= 0 {
		budgets.SkillIndexMax = defaultBudgets().SkillIndexMax
	}
	if budgets.MemoryMax <= 0 {
		budgets.MemoryMax = defaultBudgets().MemoryMax
	}
	if budgets.TotalMax <= 0 {
		budgets.TotalMax = defaultBudgets().TotalMax
	}

	// Align bootstrap loader total with bootstrap budget; keep per-file default unless larger than total.
	perFile := defaultMaxFileChars
	if perFile > budgets.BootstrapMax {
		perFile = budgets.BootstrapMax
	}
	a.bootstrap.SetMaxLimits(perFile, budgets.BootstrapMax)

	bootstrapText, err := a.bootstrap.Load()
	if err != nil {
		return "", err
	}
	if len(bootstrapText) > budgets.BootstrapMax {
		bootstrapText = bootstrapText[:budgets.BootstrapMax]
	}

	if a.skillManager != nil {
		a.skillManager.SetScanLimits(-1, budgets.SkillIndexMax)
		if err := a.skillManager.ScanSkills(); err != nil {
			return "", err
		}
	}
	skillIndex := ""
	if a.skillManager != nil {
		skillIndex = a.skillManager.GetIndexPrompt()
	}
	if len(skillIndex) > budgets.SkillIndexMax {
		skillIndex = skillIndex[:budgets.SkillIndexMax]
	}

	mem := strings.TrimSpace(memorySection)
	if len(mem) > budgets.MemoryMax {
		mem = mem[:budgets.MemoryMax]
	}

	var parts []string
	if bootstrapText != "" {
		parts = append(parts, strings.TrimSpace(bootstrapText))
	}
	if skillIndex != "" {
		parts = append(parts, strings.TrimSpace(skillIndex))
	}
	if mem != "" {
		parts = append(parts, "<<< SECTION: MEMORY >>>\n"+mem)
	}

	out := strings.Join(parts, "\n\n")
	if len(out) > budgets.TotalMax {
		out = out[:budgets.TotalMax]
	}
	return out, nil
}
