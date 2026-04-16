package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// defaultDangerousPatterns blocks obviously destructive or high-risk shell fragments.
// Callers may pass additional patterns via RegisterShellTools.
var defaultDangerousPatterns = []string{
	"rm -rf /",
	"rm -rf /*",
	"mkfs",
	"dd if=/dev/",
	":(){:|:&};:",
	">/dev/sd",
	"chmod -R 777 /",
	"wget http",
	"curl http",
	"powershell -enc",
	"format c:",
	"diskpart",
	"shutdown",
	"reboot",
	"halt",
}

// RegisterShellTools registers run_shell for executing commands under workspace constraints.
func RegisterShellTools(reg RegisterFunc, workspaceRoot string, dangerousPatterns []string) error {
	patterns := dangerousPatterns
	if len(patterns) == 0 {
		patterns = defaultDangerousPatterns
	}
	meta := &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "run_shell",
			Description: "Run a shell command with optional working directory (relative to workspace). stdout and stderr are combined. Uses sh -c on Unix and cmd /C on Windows.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Command line to execute (shell on Unix: sh -c)",
					},
					"cwd": map[string]interface{}{
						"type":        "string",
						"description": "Working directory relative to workspace",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Max runtime in seconds (default 60, max 600)",
					},
				},
				"required": []string{"command"},
			},
		},
		Permission: types.PermExecute,
		Source:     "builtin",
		Handler:    makeShellHandler(workspaceRoot, patterns),
	}
	return reg(meta)
}

func makeShellHandler(workspaceRoot string, patterns []string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		cmdLine := strings.TrimSpace(utils.GetString(args, "command"))
		if cmdLine == "" {
			return nil, fmt.Errorf("command is required")
		}
		if ok, reason := utils.IsDangerousCommand(cmdLine, patterns); ok {
			return &types.ToolResult{
				Name:    "run_shell",
				Content: reason,
				IsError: true,
			}, nil
		}

		timeoutSec := utils.GetInt(args, "timeout_seconds")
		if timeoutSec <= 0 {
			timeoutSec = 60
		}
		if timeoutSec > 600 {
			timeoutSec = 600
		}

		cwdRel := utils.GetString(args, "cwd")
		var workDir string
		var err error
		if cwdRel != "" {
			workDir, err = utils.SafePath(workspaceRoot, cwdRel)
			if err != nil {
				return nil, err
			}
		} else {
			workDir, err = utils.SafePath(workspaceRoot, ".")
			if err != nil {
				return nil, err
			}
		}

		runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		var c *exec.Cmd
		if runtime.GOOS == "windows" {
			c = exec.CommandContext(runCtx, "cmd", "/C", cmdLine)
		} else {
			c = exec.CommandContext(runCtx, "sh", "-c", cmdLine)
		}
		c.Dir = workDir

		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr

		runErr := c.Run()
		out := stdout.String()
		if stderr.Len() > 0 {
			if out != "" {
				out += "\n"
			}
			out += stderr.String()
		}
		out = strings.TrimSpace(out)

		if runErr != nil {
			if out != "" {
				out = fmt.Sprintf("%v\n%s", runErr, out)
			} else {
				out = runErr.Error()
			}
			return &types.ToolResult{Name: "run_shell", Content: out, IsError: true}, nil
		}
		return &types.ToolResult{Name: "run_shell", Content: out}, nil
	}
}
