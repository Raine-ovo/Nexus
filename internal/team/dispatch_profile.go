package team

import (
	"fmt"
	"strings"

	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

const (
	dispatchProfileOpenTag  = "<dispatch_profile>"
	dispatchProfileCloseTag = "</dispatch_profile>"
)

type dispatchProfile struct {
	Simple           bool
	NeedsPersistence bool
	NeedsIsolation   bool
	ExpectedFollowUp bool
	SpecialistRole   string
	Reason           string
}

func (p *dispatchProfile) logValue() string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf(
		"simple=%t persistence=%t isolation=%t follow_up=%t specialist=%s reason=%s",
		p.Simple,
		p.NeedsPersistence,
		p.NeedsIsolation,
		p.ExpectedFollowUp,
		p.SpecialistRole,
		p.Reason,
	)
}

func parseDispatchProfile(content string) (*dispatchProfile, error) {
	start := strings.Index(content, dispatchProfileOpenTag)
	if start == -1 {
		return nil, fmt.Errorf("missing %s block", dispatchProfileOpenTag)
	}
	start += len(dispatchProfileOpenTag)
	end := strings.Index(content[start:], dispatchProfileCloseTag)
	if end == -1 {
		return nil, fmt.Errorf("missing %s block", dispatchProfileCloseTag)
	}
	body := strings.TrimSpace(content[start : start+end])
	if body == "" {
		return nil, fmt.Errorf("empty dispatch_profile block")
	}

	profile := &dispatchProfile{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "simple":
			profile.Simple = parseProfileBool(value)
		case "needs_persistence":
			profile.NeedsPersistence = parseProfileBool(value)
		case "needs_isolation":
			profile.NeedsIsolation = parseProfileBool(value)
		case "expected_follow_up":
			profile.ExpectedFollowUp = parseProfileBool(value)
		case "specialist_role":
			profile.SpecialistRole = normalizeProfileValue(value)
		case "reason":
			profile.Reason = normalizeProfileValue(value)
		}
	}

	if err := profile.validate(); err != nil {
		return nil, err
	}
	return profile, nil
}

func (p *dispatchProfile) validate() error {
	if p == nil {
		return fmt.Errorf("nil dispatch profile")
	}
	if p.Simple && (p.NeedsPersistence || p.NeedsIsolation || p.ExpectedFollowUp || p.SpecialistRole != "") {
		return fmt.Errorf("simple=true cannot be combined with persistence, isolation, follow-up, or specialist role")
	}
	if p.NeedsPersistence && p.NeedsIsolation {
		return fmt.Errorf("needs_persistence and needs_isolation cannot both be true")
	}
	if p.NeedsIsolation && p.ExpectedFollowUp {
		return fmt.Errorf("needs_isolation=true cannot be combined with expected_follow_up=true")
	}
	return nil
}

func parseProfileBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}

func normalizeProfileValue(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"'`)
	return v
}

func isLeadRoutingTool(name string) bool {
	switch name {
	case "delegate_task", "spawn_teammate", "send_message":
		return true
	default:
		return false
	}
}

func findReusableTeammateForRole(roster *Roster, role string) string {
	if roster == nil {
		return ""
	}
	role = strings.TrimSpace(role)
	for _, member := range roster.List() {
		if member.Name == leadName || member.Status == StatusShutdown {
			continue
		}
		if role == "" || member.Role == role {
			return member.Name
		}
	}
	return ""
}

func validateLeadRoutingCall(profile *dispatchProfile, roster *Roster, call types.ToolCall) error {
	if profile == nil {
		return fmt.Errorf("missing dispatch_profile: before using delegate_task/spawn_teammate/send_message, emit a <dispatch_profile> block with simple, needs_persistence, needs_isolation, expected_follow_up, specialist_role, and reason")
	}
	if profile.Simple {
		return fmt.Errorf("dispatch_profile says simple=true, so do not use team routing tools; handle the task directly")
	}

	reusable := findReusableTeammateForRole(roster, profile.SpecialistRole)
	target := utils.GetString(call.Arguments, "to")
	roleArg := utils.GetString(call.Arguments, "role")

	if profile.NeedsIsolation {
		if call.Name != "delegate_task" {
			return fmt.Errorf("dispatch_profile requires isolation, so use delegate_task instead of %s", call.Name)
		}
		return nil
	}

	if profile.NeedsPersistence || profile.ExpectedFollowUp {
		switch call.Name {
		case "delegate_task":
			if reusable != "" {
				return fmt.Errorf("dispatch_profile requires persistence/follow-up; reuse teammate %q via send_message instead of delegate_task", reusable)
			}
			return fmt.Errorf("dispatch_profile requires persistence/follow-up; create a persistent teammate with spawn_teammate instead of delegate_task")
		case "spawn_teammate":
			if reusable != "" {
				return fmt.Errorf("teammate %q already matches this direction; reuse it via send_message instead of spawning a new one", reusable)
			}
			if profile.SpecialistRole != "" && roleArg != "" && roleArg != profile.SpecialistRole {
				return fmt.Errorf("spawned teammate role %q does not match dispatch_profile specialist_role=%q", roleArg, profile.SpecialistRole)
			}
			return nil
		case "send_message":
			if target == "" {
				return fmt.Errorf("send_message requires a target teammate")
			}
			member, ok := roster.Get(target)
			if !ok {
				return fmt.Errorf("teammate %q does not exist; spawn a persistent teammate first", target)
			}
			if member.Name == leadName || member.Status == StatusShutdown {
				return fmt.Errorf("teammate %q is not an active persistent worker", target)
			}
			if profile.SpecialistRole != "" && member.Role != profile.SpecialistRole {
				return fmt.Errorf("send_message target %q has role %q, expected specialist_role=%q", target, member.Role, profile.SpecialistRole)
			}
			return nil
		default:
			return nil
		}
	}

	return fmt.Errorf("dispatch_profile does not justify team routing yet; handle directly or mark persistence/isolation explicitly")
}

func dispatchModeForCall(profile *dispatchProfile, roster *Roster, call types.ToolCall) string {
	if profile == nil {
		return "missing_profile"
	}
	if profile.Simple {
		return "direct"
	}
	if profile.NeedsIsolation {
		return "delegate"
	}
	if profile.NeedsPersistence || profile.ExpectedFollowUp {
		if call.Name == "send_message" {
			return "message_existing"
		}
		if call.Name == "spawn_teammate" {
			if findReusableTeammateForRole(roster, profile.SpecialistRole) != "" {
				return "spawn_blocked_reuse_exists"
			}
			return "spawn_teammate"
		}
		return "persistent_required"
	}
	return "direct"
}
