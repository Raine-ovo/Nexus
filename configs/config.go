package configs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Model         ModelConfig         `yaml:"model"`
	Agent         AgentConfig         `yaml:"agent"`
	RAG           RAGConfig           `yaml:"rag"`
	Memory        MemoryConfig        `yaml:"memory"`
	Planning      PlanningConfig      `yaml:"planning"`
	Team          TeamConfig          `yaml:"team"`
	Run           RunConfig           `yaml:"run"`
	Gateway       GatewayConfig       `yaml:"gateway"`
	MCP           MCPConfig           `yaml:"mcp"`
	Permission    PermissionConfig    `yaml:"permission"`
	Observability ObservabilityConfig `yaml:"observability"`
	Reflection    ReflectionConfig    `yaml:"reflection"`
}

type ServerConfig struct {
	HTTPAddr     string        `yaml:"http_addr"`
	WSAddr       string        `yaml:"ws_addr"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type ModelConfig struct {
	Provider    string  `yaml:"provider"`
	ModelName   string  `yaml:"model_name"`
	APIKey      string  `yaml:"api_key"`
	BaseURL     string  `yaml:"base_url"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
	// MaxConcurrency limits concurrent LLM requests process-wide for this configured model.
	// 0 disables throttling.
	MaxConcurrency int `yaml:"max_concurrency"`
	// MinRequestIntervalMS enforces a minimum spacing between process-wide LLM requests.
	// 0 disables interval throttling.
	MinRequestIntervalMS int `yaml:"min_request_interval_ms"`
}

type AgentConfig struct {
	MaxIterations      int     `yaml:"max_iterations"`
	TokenThreshold     int     `yaml:"token_threshold"`
	CompactTargetRatio float64 `yaml:"compact_target_ratio"`
	MicroCompactSize   int     `yaml:"micro_compact_size"`
	OutputPersistDir   string  `yaml:"output_persist_dir"`
}

type RAGConfig struct {
	ChunkSize    int    `yaml:"chunk_size"`
	ChunkOverlap int    `yaml:"chunk_overlap"`
	EmbeddingDim int    `yaml:"embedding_dim"`
	TopK         int    `yaml:"top_k"`
	RerankTopK   int    `yaml:"rerank_top_k"`
	KnowledgeDir string `yaml:"knowledge_dir"`

	// VectorBackend selects the vector store: "memory" (default) or "milvus".
	VectorBackend string      `yaml:"vector_backend"`
	Milvus        MilvusStore `yaml:"milvus"`

	// KeywordBackend selects the keyword store: "memory" (default, TF-IDF) or "elasticsearch" (BM25).
	KeywordBackend string             `yaml:"keyword_backend"`
	Elasticsearch  ElasticsearchStore `yaml:"elasticsearch"`
}

// ElasticsearchStore holds Elasticsearch-specific configuration for BM25 keyword retrieval.
type ElasticsearchStore struct {
	Addresses  []string `yaml:"addresses"`
	IndexName  string   `yaml:"index_name"`
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password"`
	Shards     int      `yaml:"shards"`
	Replicas   int      `yaml:"replicas"`
	BM25K1     float64  `yaml:"bm25_k1"`
	BM25B      float64  `yaml:"bm25_b"`
	AutoCreate bool     `yaml:"auto_create"`
}

// MilvusStore holds Milvus-specific configuration.
type MilvusStore struct {
	Address              string `yaml:"address"`
	CollectionName       string `yaml:"collection_name"`
	MetricType           string `yaml:"metric_type"`
	IndexType            string `yaml:"index_type"`
	Nlist                int    `yaml:"nlist"`
	Nprobe               int    `yaml:"nprobe"`
	EfConstruction       int    `yaml:"ef_construction"`
	Ef                   int    `yaml:"ef"`
	ConsistencyLevel     string `yaml:"consistency_level"`
	AutoCreateCollection bool   `yaml:"auto_create_collection"`
}

type MemoryConfig struct {
	ConversationWindow  int    `yaml:"conversation_window"`
	MaxSemanticEntries  int    `yaml:"max_semantic_entries"`
	SemanticFile        string `yaml:"semantic_file"`
	CompactionThreshold int    `yaml:"compaction_threshold"`
}

type PlanningConfig struct {
	TaskDir            string        `yaml:"task_dir"`
	MaxBackgroundSlots int           `yaml:"max_background_slots"`
	CronPollInterval   time.Duration `yaml:"cron_poll_interval"`
}

type TeamConfig struct {
	Dir string `yaml:"dir"`
	// PollInterval controls how often persistent teammates poll inbox/tasks while idle.
	PollInterval time.Duration `yaml:"poll_interval"`
	// IdleTimeout controls how long a persistent teammate may stay idle before shutdown.
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// ScopeManagerTTL controls how long an inactive scope manager stays resident in memory.
	// Scope data on disk remains intact; a later request simply rehydrates the manager on demand.
	ScopeManagerTTL time.Duration `yaml:"scope_manager_ttl"`
}

type RunConfig struct {
	SandboxDir string `yaml:"sandbox_dir"`
}

type GatewayConfig struct {
	Lanes     map[string]LaneConfig `yaml:"lanes"`
	Auth      GatewayAuthConfig     `yaml:"auth"`
	RateLimit RateLimitConfig       `yaml:"rate_limit"`
}

type LaneConfig struct {
	MaxConcurrency int `yaml:"max_concurrency"`
}

type GatewayAuthConfig struct {
	APIKeys   []string `yaml:"api_keys"`
	JWTSecret string   `yaml:"jwt_secret"`
}

type RateLimitConfig struct {
	Enabled bool    `yaml:"enabled"`
	RPS     float64 `yaml:"rps"`
	Burst   int     `yaml:"burst"`
}

type MCPConfig struct {
	ServerEnabled bool              `yaml:"server_enabled"`
	RPCPath       string            `yaml:"rpc_path"`
	SSEPath       string            `yaml:"sse_path"`
	Clients       []MCPClientConfig `yaml:"clients"`
}

type MCPClientConfig struct {
	Name       string `yaml:"name"`
	Enabled    bool   `yaml:"enabled"`
	BaseURL    string `yaml:"base_url"`
	RPCPath    string `yaml:"rpc_path"`
	SSEPath    string `yaml:"sse_path"`
	ConnectSSE bool   `yaml:"connect_sse"`
}

type PermissionConfig struct {
	Mode              string   `yaml:"mode"`
	WorkspaceRoot     string   `yaml:"workspace_root"`
	DangerousPatterns []string `yaml:"dangerous_patterns"`
}

type ObservabilityConfig struct {
	TraceEnabled   bool   `yaml:"trace_enabled"`
	MetricsEnabled bool   `yaml:"metrics_enabled"`
	LogLevel       string `yaml:"log_level"`
}

// ReflectionConfig controls the three-phase reflection engine.
type ReflectionConfig struct {
	Enabled        bool    `yaml:"enabled"`
	MaxAttempts    int     `yaml:"max_attempts"`
	Threshold      float64 `yaml:"threshold"`
	EnableProspect bool    `yaml:"enable_prospect"`
	MemoryFile     string  `yaml:"memory_file"`
	MaxMemEntries  int     `yaml:"max_mem_entries"`
}

// Load reads and parses a YAML config file, expanding environment variables.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	expanded := os.Expand(string(data), func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return "${" + key + "}"
	})

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	applyDefaults(cfg)
	return cfg, nil
}

// LoadFromEnv constructs a minimal config from environment variables.
func LoadFromEnv() *Config {
	cfg := &Config{}
	applyDefaults(cfg)

	if v := os.Getenv("NEXUS_API_KEY"); v != "" {
		cfg.Model.APIKey = v
	}
	if v := os.Getenv("NEXUS_BASE_URL"); v != "" {
		cfg.Model.BaseURL = v
	}
	if v := os.Getenv("NEXUS_MODEL"); v != "" {
		cfg.Model.ModelName = v
	}
	if v := os.Getenv("NEXUS_HTTP_ADDR"); v != "" {
		cfg.Server.HTTPAddr = v
	}
	if v := os.Getenv("NEXUS_LOG_LEVEL"); v != "" {
		cfg.Observability.LogLevel = strings.ToLower(v)
	}
	if v := os.Getenv("NEXUS_RUN_SANDBOX"); v != "" {
		cfg.Run.SandboxDir = v
	}
	if v := os.Getenv("NEXUS_TEAM_DIR"); v != "" {
		cfg.Team.Dir = v
	}

	return cfg
}

func applyDefaults(cfg *Config) {
	if cfg.Server.HTTPAddr == "" {
		cfg.Server.HTTPAddr = ":8080"
	}
	if cfg.Server.WSAddr == "" {
		cfg.Server.WSAddr = ":8081"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 30 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30 * time.Second
	}
	if cfg.Model.Provider == "" {
		cfg.Model.Provider = "openai"
	}
	if cfg.Model.ModelName == "" {
		cfg.Model.ModelName = "gpt-4o"
	}
	if cfg.Model.MaxTokens == 0 {
		cfg.Model.MaxTokens = 8192
	}
	if cfg.Model.Temperature == 0 {
		cfg.Model.Temperature = 0.7
	}
	if cfg.Agent.MaxIterations == 0 {
		cfg.Agent.MaxIterations = 20
	}
	if cfg.Agent.TokenThreshold == 0 {
		cfg.Agent.TokenThreshold = 100000
	}
	if cfg.Agent.CompactTargetRatio == 0 {
		cfg.Agent.CompactTargetRatio = 0.6
	}
	if cfg.Agent.MicroCompactSize == 0 {
		cfg.Agent.MicroCompactSize = 51200
	}
	if cfg.Agent.OutputPersistDir == "" {
		cfg.Agent.OutputPersistDir = ".outputs"
	}
	if cfg.RAG.ChunkSize == 0 {
		cfg.RAG.ChunkSize = 512
	}
	if cfg.RAG.ChunkOverlap == 0 {
		cfg.RAG.ChunkOverlap = 64
	}
	if cfg.RAG.EmbeddingDim == 0 {
		cfg.RAG.EmbeddingDim = 1536
	}
	if cfg.RAG.TopK == 0 {
		cfg.RAG.TopK = 5
	}
	if cfg.RAG.RerankTopK == 0 {
		cfg.RAG.RerankTopK = 3
	}
	if cfg.RAG.KnowledgeDir == "" {
		cfg.RAG.KnowledgeDir = ".knowledge"
	}
	if cfg.RAG.VectorBackend == "" {
		cfg.RAG.VectorBackend = "memory"
	}
	if cfg.RAG.Milvus.MetricType == "" {
		cfg.RAG.Milvus.MetricType = "COSINE"
	}
	if cfg.RAG.Milvus.IndexType == "" {
		cfg.RAG.Milvus.IndexType = "IVF_FLAT"
	}
	if cfg.RAG.Milvus.CollectionName == "" {
		cfg.RAG.Milvus.CollectionName = "nexus_vectors"
	}
	if cfg.RAG.KeywordBackend == "" {
		cfg.RAG.KeywordBackend = "memory"
	}
	if len(cfg.RAG.Elasticsearch.Addresses) == 0 {
		cfg.RAG.Elasticsearch.Addresses = []string{"http://localhost:9200"}
	}
	if cfg.RAG.Elasticsearch.IndexName == "" {
		cfg.RAG.Elasticsearch.IndexName = "nexus_chunks"
	}
	if cfg.RAG.Elasticsearch.Shards == 0 {
		cfg.RAG.Elasticsearch.Shards = 1
	}
	if cfg.RAG.Elasticsearch.BM25K1 == 0 {
		cfg.RAG.Elasticsearch.BM25K1 = 1.2
	}
	if cfg.RAG.Elasticsearch.BM25B == 0 {
		cfg.RAG.Elasticsearch.BM25B = 0.75
	}
	if cfg.Memory.ConversationWindow == 0 {
		cfg.Memory.ConversationWindow = 20
	}
	if cfg.Memory.MaxSemanticEntries == 0 {
		cfg.Memory.MaxSemanticEntries = 500
	}
	if cfg.Memory.SemanticFile == "" {
		cfg.Memory.SemanticFile = ".memory/semantic.yaml"
	}
	if cfg.Memory.CompactionThreshold == 0 {
		cfg.Memory.CompactionThreshold = 80000
	}
	if cfg.Planning.TaskDir == "" {
		cfg.Planning.TaskDir = ".tasks"
	}
	if cfg.Planning.MaxBackgroundSlots == 0 {
		cfg.Planning.MaxBackgroundSlots = 3
	}
	if cfg.Planning.CronPollInterval == 0 {
		cfg.Planning.CronPollInterval = 60 * time.Second
	}
	if cfg.Team.Dir == "" {
		cfg.Team.Dir = ".team"
	}
	if cfg.Team.PollInterval == 0 {
		cfg.Team.PollInterval = 3 * time.Second
	}
	if cfg.Team.IdleTimeout == 0 {
		cfg.Team.IdleTimeout = 20 * time.Minute
	}
	if cfg.Team.ScopeManagerTTL == 0 {
		cfg.Team.ScopeManagerTTL = 45 * time.Minute
	}
	if cfg.Gateway.Lanes == nil {
		cfg.Gateway.Lanes = map[string]LaneConfig{
			"main":       {MaxConcurrency: 1},
			"cron":       {MaxConcurrency: 1},
			"background": {MaxConcurrency: 3},
		}
	}
	if !cfg.Gateway.RateLimit.Enabled {
		cfg.Gateway.RateLimit.RPS = 0
		cfg.Gateway.RateLimit.Burst = 0
	}
	if cfg.MCP.RPCPath == "" {
		cfg.MCP.RPCPath = "/mcp/rpc"
	}
	if cfg.MCP.SSEPath == "" {
		cfg.MCP.SSEPath = "/mcp/sse"
	}
	if cfg.Model.MaxConcurrency < 0 {
		cfg.Model.MaxConcurrency = 0
	}
	if cfg.Model.MinRequestIntervalMS < 0 {
		cfg.Model.MinRequestIntervalMS = 0
	}
	if cfg.Permission.Mode == "" {
		cfg.Permission.Mode = "semi_auto"
	}
	if cfg.Permission.WorkspaceRoot == "" {
		cfg.Permission.WorkspaceRoot = "."
	}
	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}
	if cfg.Reflection.MaxAttempts == 0 {
		cfg.Reflection.MaxAttempts = 3
	}
	if cfg.Reflection.Threshold == 0 {
		cfg.Reflection.Threshold = 0.7
	}
	if cfg.Reflection.MemoryFile == "" {
		cfg.Reflection.MemoryFile = ".memory/reflections.yaml"
	}
	if cfg.Reflection.MaxMemEntries == 0 {
		cfg.Reflection.MaxMemEntries = 200
	}
}
