// Package devops provides a BaseAgent for infrastructure and operations workflows.
package devops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// DevOpsAgent handles infra-style tasks using HTTP, log parsing, and curated diagnostics.
type DevOpsAgent struct {
	*core.BaseAgent
}

// New registers DevOps-specific tools on a BaseAgent.
func New(deps *core.AgentDependencies) *DevOpsAgent {
	ws := "."
	if deps != nil && deps.WorkspaceRoot != "" {
		ws = deps.WorkspaceRoot
	}
	ba := core.NewBaseAgent(deps, core.DefaultChatModel,
		"devops",
		"Runs health checks, parses logs, and executes safe diagnostic presets for operations workflows.",
		SystemPrompt,
		ws,
	)
	d := &DevOpsAgent{BaseAgent: ba}
	_ = d.BaseAgent.RegisterTools(
		d.toolCheckHealth(),
		d.toolParseLogs(),
		d.toolRunDiagnostic(ws),
	)
	attachRegistryTools(ba, deps, "run_shell", "http_request", "read_file", "grep_search", "load_skill", "list_skills")
	return d
}

func attachRegistryTools(ba *core.BaseAgent, deps *core.AgentDependencies, names ...string) {
	if ba == nil || deps == nil || deps.ToolRegistry == nil {
		return
	}
	for _, name := range names {
		if m := deps.ToolRegistry.Get(name); m != nil {
			_ = ba.AddTool(m)
		}
	}
}

func toolResult(content string, err error) (*types.ToolResult, error) {
	if err != nil {
		return &types.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return &types.ToolResult{Content: content, IsError: false}, nil
}

func strArg(args map[string]interface{}, key string) string {
	return utils.GetString(args, key)
}

func intArg(args map[string]interface{}, key string) int {
	return utils.GetInt(args, key)
}

func (d *DevOpsAgent) toolCheckHealth() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "check_health",
			Description: "Perform an HTTP GET (or HEAD) against a URL and report status, timing, and truncated body.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Fully qualified http(s) URL",
					},
					"method": map[string]interface{}{
						"type":        "string",
						"description": "GET or HEAD (default GET)",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Timeout in seconds (default 15, max 60)",
					},
				},
				"required": []string{"url"},
			},
		},
		Permission: types.PermNetwork,
		Source:     "agent/devops",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			rawURL := strings.TrimSpace(strArg(args, "url"))
			if rawURL == "" {
				return toolResult("", fmt.Errorf("url is required"))
			}
			if !strings.HasPrefix(strings.ToLower(rawURL), "http://") && !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
				return toolResult("", fmt.Errorf("url must start with http:// or https://"))
			}
			method := strings.ToUpper(strings.TrimSpace(strArg(args, "method")))
			if method == "" {
				method = http.MethodGet
			}
			if method != http.MethodGet && method != http.MethodHead {
				return toolResult("", fmt.Errorf("method must be GET or HEAD"))
			}
			to := intArg(args, "timeout_seconds")
			if to <= 0 {
				to = 15
			}
			if to > 60 {
				to = 60
			}
			cctx, cancel := context.WithTimeout(ctx, time.Duration(to)*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(cctx, method, rawURL, nil)
			if err != nil {
				return toolResult("", err)
			}
			start := time.Now()
			resp, err := http.DefaultClient.Do(req)
			elapsed := time.Since(start).Milliseconds()
			if err != nil {
				return toolResult("", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			text := string(body)
			if len(text) > 4000 {
				text = text[:4000] + "…"
			}
			out := map[string]interface{}{
				"url":             rawURL,
				"method":          method,
				"status_code":     resp.StatusCode,
				"status":          resp.Status,
				"latency_ms":      elapsed,
				"content_length":  resp.ContentLength,
				"body_snippet":    text,
				"content_type":    resp.Header.Get("Content-Type"),
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

var logLevelRe = regexp.MustCompile(`(?i)\b(ERROR|WARN|WARNING|INFO|DEBUG|FATAL|PANIC|TRACE)\b`)

func (d *DevOpsAgent) toolParseLogs() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "parse_logs",
			Description: "Summarize raw log text: counts by level, top repeated lines, and notable stack trace hints.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"log_text": map[string]interface{}{
						"type":        "string",
						"description": "Log lines to analyze",
					},
					"max_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Max lines to process (default 5000)",
					},
				},
				"required": []string{"log_text"},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/devops",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			text := strArg(args, "log_text")
			maxLines := intArg(args, "max_lines")
			if maxLines <= 0 {
				maxLines = 5000
			}
			lines := strings.Split(text, "\n")
			if len(lines) > maxLines {
				lines = lines[:maxLines]
			}
			levelCounts := map[string]int{}
			lineFreq := map[string]int{}
			exceptions := 0
			for _, ln := range lines {
				trim := strings.TrimSpace(ln)
				if trim == "" {
					continue
				}
				if m := logLevelRe.FindStringSubmatch(trim); len(m) > 1 {
					levelCounts[strings.ToUpper(m[1])]++
				}
				lt := strings.ToLower(trim)
				if strings.Contains(lt, "exception") || strings.Contains(lt, "stacktrace") || strings.Contains(lt, "panic") {
					exceptions++
				}
				key := trim
				if len(key) > 160 {
					key = key[:160] + "…"
				}
				lineFreq[key]++
			}
			type freq struct {
				Line  string `json:"line"`
				Count int    `json:"count"`
			}
			var tops []freq
			for l, c := range lineFreq {
				if c < 2 && len(lineFreq) > 50 {
					continue
				}
				tops = append(tops, freq{Line: l, Count: c})
			}
			sort.Slice(tops, func(i, j int) bool {
				if tops[i].Count == tops[j].Count {
					return tops[i].Line < tops[j].Line
				}
				return tops[i].Count > tops[j].Count
			})
			if len(tops) > 20 {
				tops = tops[:20]
			}
			out := map[string]interface{}{
				"lines_processed": len(lines),
				"level_counts":    levelCounts,
				"exception_hits":  exceptions,
				"top_repeated":    tops,
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (d *DevOpsAgent) toolRunDiagnostic(workspaceRoot string) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "run_diagnostic",
			Description: "Run a whitelisted set of read-only shell probes in the workspace (quick, runtime, or network preset).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"preset": map[string]interface{}{
						"type":        "string",
						"description": "quick | runtime | network",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Per-command timeout (default 10, max 30)",
					},
				},
				"required": []string{"preset"},
			},
		},
		Permission: types.PermExecute,
		Source:     "agent/devops",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			preset := strings.ToLower(strings.TrimSpace(strArg(args, "preset")))
			to := intArg(args, "timeout_seconds")
			if to <= 0 {
				to = 10
			}
			if to > 30 {
				to = 30
			}
			var cmds [][]string
			switch preset {
			case "quick":
				cmds = [][]string{{"pwd"}, {"date", "-u"}}
			case "runtime":
				cmds = [][]string{{"uname", "-a"}}
			case "network":
				// Avoid ping (may be blocked); use DNS via go isn't needed — use harmless `hostname`
				cmds = [][]string{{"hostname"}}
			default:
				return toolResult("", fmt.Errorf("unknown preset %q (use quick, runtime, network)", preset))
			}

			type cmdOut struct {
				Args   []string `json:"args"`
				Stdout string   `json:"stdout"`
				Stderr string   `json:"stderr"`
				Error  string   `json:"error,omitempty"`
			}
			var results []cmdOut
			for _, c := range cmds {
				cctx, cancel := context.WithTimeout(ctx, time.Duration(to)*time.Second)
				cmd := exec.CommandContext(cctx, c[0], c[1:]...)
				cmd.Dir = workspaceRoot
				out, err := cmd.CombinedOutput()
				cancel()
				co := cmdOut{Args: c, Stdout: string(out)}
				if err != nil {
					co.Error = err.Error()
				}
				results = append(results, co)
			}
			payload := map[string]interface{}{
				"preset":  preset,
				"results": results,
			}
			b, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}
