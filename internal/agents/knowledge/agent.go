// Package knowledge provides a RAG-focused BaseAgent specialization.
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/rag"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// KnowledgeAgent answers questions using the RAG engine and optional knowledge-base metadata.
type KnowledgeAgent struct {
	*core.BaseAgent
	ragEngine *rag.Engine
}

// New constructs the knowledge agent. ragEngine may be nil; tools will report a clear error until configured.
func New(deps *core.AgentDependencies, ragEngine *rag.Engine) *KnowledgeAgent {
	ws := "."
	if deps != nil && deps.WorkspaceRoot != "" {
		ws = deps.WorkspaceRoot
	}
	ba := core.NewBaseAgent(deps, core.DefaultChatModel,
		"knowledge",
		"Searches and reasons over the indexed knowledge base using RAG; can ingest documents when asked.",
		SystemPrompt,
		ws,
	)
	k := &KnowledgeAgent{
		BaseAgent: ba,
		ragEngine: ragEngine,
	}
	_ = k.BaseAgent.RegisterTools(
		k.toolSearchKnowledge(),
		k.toolIngestDocument(ws),
		k.toolListKnowledgeBases(),
	)
	attachRegistryTools(ba, deps, "load_skill", "list_skills")
	return k
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

func (k *KnowledgeAgent) toolSearchKnowledge() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "search_knowledge",
			Description: "Query the RAG index with natural language. Returns ranked chunks with scores, doc IDs, and text for citation.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query",
					},
					"top_k": map[string]interface{}{
						"type":        "integer",
						"description": "Number of chunks to return (default from engine config)",
					},
				},
				"required": []string{"query"},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/knowledge",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			if k.ragEngine == nil {
				return toolResult("", fmt.Errorf("rag engine not configured"))
			}
			q := strings.TrimSpace(strArg(args, "query"))
			if q == "" {
				return toolResult("", fmt.Errorf("query is required"))
			}
			topK := intArg(args, "top_k")
			chunks, err := k.ragEngine.QueryChunks(ctx, q, topK)
			if err != nil {
				return toolResult("", err)
			}
			type chunkDTO struct {
				ChunkID  string                 `json:"chunk_id"`
				DocID    string                 `json:"doc_id"`
				Score    float64                `json:"score"`
				Channel  string                 `json:"channel"`
				Preview  string                 `json:"preview"`
				Metadata map[string]interface{} `json:"metadata,omitempty"`
			}
			dtos := make([]chunkDTO, 0, len(chunks))
			for _, ch := range chunks {
				src := ""
				if ch.Metadata != nil {
					if s, ok := ch.Metadata["source"].(string); ok {
						src = s
					}
				}
				prev := strings.TrimSpace(ch.Content)
				if len(prev) > 1200 {
					prev = prev[:1200] + "…"
				}
				md := ch.Metadata
				dtos = append(dtos, chunkDTO{
					ChunkID:  ch.ChunkID,
					DocID:    firstNonEmpty(ch.DocID, src),
					Score:    ch.Score,
					Channel:  ch.Channel,
					Preview:  prev,
					Metadata: md,
				})
			}
			formatted, ferr := k.ragEngine.Query(ctx, q, topK)
			if ferr != nil {
				formatted = ""
			}
			out := map[string]interface{}{
				"chunks":            dtos,
				"formatted_context": formatted,
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (k *KnowledgeAgent) toolIngestDocument(workspaceRoot string) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "ingest_document",
			Description: "Index a single file from the workspace into the RAG vector + keyword stores.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File path relative to workspace root",
					},
				},
				"required": []string{"path"},
			},
		},
		Permission: types.PermWrite,
		Source:     "agent/knowledge",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			if k.ragEngine == nil {
				return toolResult("", fmt.Errorf("rag engine not configured"))
			}
			rel := strArg(args, "path")
			if rel == "" {
				return toolResult("", fmt.Errorf("path is required"))
			}
			abs, err := utils.SafePath(workspaceRoot, rel)
			if err != nil {
				return toolResult("", err)
			}
			if err := k.ragEngine.Ingest(ctx, abs); err != nil {
				return toolResult("", err)
			}
			msg := map[string]interface{}{
				"status":  "indexed",
				"path":    rel,
				"abs_path": abs,
			}
			b, err := json.MarshalIndent(msg, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (k *KnowledgeAgent) toolListKnowledgeBases() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "list_knowledge_bases",
			Description: "List logical knowledge collections exposed to this agent (includes the primary in-process RAG index).",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/knowledge",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			_ = args
			bases := []map[string]interface{}{
				{
					"id":          "default_rag",
					"type":        "vector_and_keyword",
					"description": "Primary Nexus RAG Engine (MemoryVectorStore + keyword index)",
				},
			}
			if k.ragEngine == nil {
				bases = append([]map[string]interface{}{{
					"id":          "unavailable",
					"type":        "none",
					"description": "RAG engine is not initialized; search and ingest tools will fail until configured",
				}}, bases...)
			}
			b, err := json.MarshalIndent(map[string]interface{}{"knowledge_bases": bases}, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}
