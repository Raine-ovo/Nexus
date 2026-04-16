package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

const maxHTTPResponseRunes = 200_000

// allowedHTTPMethods lists verbs accepted by http_request (case-insensitive).
var allowedHTTPMethods = map[string]struct{}{
	http.MethodGet:    {},
	http.MethodPost:   {},
	http.MethodPut:    {},
	http.MethodPatch:  {},
	http.MethodDelete: {},
	http.MethodHead:   {},
}

// RegisterHTTPTools registers http_request for outbound HTTP calls.
func RegisterHTTPTools(reg RegisterFunc) error {
	meta := &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "http_request",
			Description: "Perform an HTTP request (GET, POST, PUT, PATCH, DELETE, HEAD). Supports JSON object body via json_body; otherwise raw body string. Large responses are truncated.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"method": map[string]interface{}{
						"type":        "string",
						"description": "GET, POST, PUT, PATCH, DELETE, HEAD",
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Full URL",
					},
					"headers": map[string]interface{}{
						"type":        "object",
						"description": "Header map string -> string",
					},
					"body": map[string]interface{}{
						"type":        "string",
						"description": "Raw request body (ignored if json_body set)",
					},
					"json_body": map[string]interface{}{
						"type":        "object",
						"description": "JSON object serialized as application/json",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Request timeout (default 30, max 120)",
					},
				},
				"required": []string{"method", "url"},
			},
		},
		Permission: types.PermNetwork,
		Source:     "builtin",
		Handler:    httpRequestHandler,
	}
	return reg(meta)
}

func httpRequestHandler(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
	method := strings.ToUpper(strings.TrimSpace(utils.GetString(args, "method")))
	rawURL := strings.TrimSpace(utils.GetString(args, "url"))
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if method == "" {
		method = http.MethodGet
	}
	if _, ok := allowedHTTPMethods[method]; !ok {
		return nil, fmt.Errorf("unsupported HTTP method %q", method)
	}

	timeoutSec := utils.GetInt(args, "timeout_seconds")
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 120 {
		timeoutSec = 120
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var bodyReader io.Reader
	headers := make(http.Header)

	if hmap, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range hmap {
			if s, ok := v.(string); ok {
				headers.Set(k, s)
			}
		}
	}

	if jb, ok := args["json_body"].(map[string]interface{}); ok && jb != nil {
		b, err := json.Marshal(jb)
		if err != nil {
			return nil, fmt.Errorf("json_body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json")
		}
	} else if s := utils.GetString(args, "body"); s != "" {
		bodyReader = strings.NewReader(s)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header = headers

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	const maxRead = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRead))
	if err != nil {
		return nil, err
	}

	var preview string
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(ct), "application/json") {
		var buf bytes.Buffer
		if err := json.Indent(&buf, data, "", "  "); err == nil {
			preview = buf.String()
		} else {
			preview = string(data)
		}
	} else {
		preview = string(data)
	}

	preview = utils.TruncateString(preview, maxHTTPResponseRunes)

	var b strings.Builder
	fmt.Fprintf(&b, "HTTP %d %s\n", resp.StatusCode, resp.Status)
	for k, vals := range resp.Header {
		for _, v := range vals {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	b.WriteString("\n")
	b.WriteString(preview)

	return &types.ToolResult{Name: "http_request", Content: b.String()}, nil
}
