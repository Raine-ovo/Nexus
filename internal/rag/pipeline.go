package rag

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/rag/chunker"
	"github.com/rainea/nexus/internal/rag/index"
	"github.com/rainea/nexus/internal/rag/loader"
	"github.com/rainea/nexus/internal/rag/retrieval"
	"github.com/rainea/nexus/pkg/types"
)

// Embedder converts text into dense vectors; swap implementations for real embedding models.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	Dimensions() int
}

// HashEmbedder is a deterministic placeholder embedder: SHA-256(text) seeds a PRNG that fills
// a vector, then L2-normalizes. Same input text always yields the same embedding for a fixed dim,
// without calling external models. Suitable for tests and offline pipelines.
type HashEmbedder struct {
	dim int
}

// NewHashEmbedder creates an embedder with fixed dimensionality (must match vector store).
func NewHashEmbedder(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 128
	}
	return &HashEmbedder{dim: dim}
}

// Dimensions returns the configured vector size.
func (h *HashEmbedder) Dimensions() int {
	return h.dim
}

// Embed hashes the input and expands it into a unit vector.
func (h *HashEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return hashToUnitVector(text, h.dim), nil
}

func hashToUnitVector(text string, dim int) []float64 {
	sum := sha256.Sum256([]byte(text))
	seed := int64(binary.BigEndian.Uint64(sum[:8]))
	rng := rand.New(rand.NewSource(seed))
	v := make([]float64, dim)
	var norm float64
	for i := range v {
		v[i] = rng.NormFloat64()
		norm += v[i] * v[i]
	}
	if norm == 0 {
		v[0] = 1
		return v
	}
	inv := 1 / math.Sqrt(norm)
	for i := range v {
		v[i] *= inv
	}
	return v
}

type embedBridge struct {
	inner Embedder
}

func (b embedBridge) Embed(ctx context.Context, text string) ([]float64, error) {
	return b.inner.Embed(ctx, text)
}

func (b embedBridge) Dimensions() int {
	return b.inner.Dimensions()
}

// Engine is the main RAG entry point, orchestrating ingestion and retrieval.
type Engine struct {
	cfg       configs.RAGConfig
	indexer   index.VectorStore
	chunker   *chunker.RecursiveChunker
	loader    *loader.DirectoryLoader
	retrieval *retrieval.MultiChannelEngine

	embedder Embedder
	reranker retrieval.Reranker
	post     *retrieval.PostChain

	keywordStore retrieval.KeywordStore

	mu        sync.Mutex
	docChunks map[string][]string // source path -> chunk IDs for deletion
}

// NewEngine creates and initializes the RAG engine.
// The vector backend is selected by cfg.VectorBackend: "memory" (default) or "milvus".
func NewEngine(cfg configs.RAGConfig, emb Embedder) (*Engine, error) {
	if emb == nil {
		dim := cfg.EmbeddingDim
		if dim <= 0 {
			dim = 128
		}
		emb = NewHashEmbedder(dim)
	}
	dim := emb.Dimensions()
	if dim <= 0 {
		return nil, fmt.Errorf("rag: embedder dimensions must be positive")
	}

	vs, err := buildVectorStore(cfg, dim)
	if err != nil {
		return nil, fmt.Errorf("rag: init vector store: %w", err)
	}

	ks, err := buildKeywordStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("rag: init keyword store: %w", err)
	}
	chunkerInst := chunker.NewRecursiveChunker(cfg.ChunkSize, cfg.ChunkOverlap)

	var root string
	if cfg.KnowledgeDir != "" {
		root = cfg.KnowledgeDir
	} else {
		root = "."
	}
	dl, err := loader.NewDirectoryLoader(root)
	if err != nil {
		dl = nil
	}

	bridge := embedBridge{inner: emb}
	retr := retrieval.NewMultiChannelEngine(vs, ks, bridge)

	rerank := retrieval.NewCompositeReranker(
		retrieval.NewKeywordBoostReranker(),
		retrieval.NewCrossEncoderReranker(),
	)
	post := retrieval.NewPostChain(
		retrieval.NewDeduplicateProcessor(),
		retrieval.NewScoreNormalizer(),
		retrieval.NewContextEnricher(),
	)

	return &Engine{
		cfg:          cfg,
		indexer:      vs,
		chunker:      chunkerInst,
		loader:       dl,
		retrieval:    retr,
		embedder:     emb,
		reranker:     rerank,
		post:         post,
		keywordStore: ks,
		docChunks:    make(map[string][]string),
	}, nil
}

// buildKeywordStore creates the appropriate KeywordStore based on config.
func buildKeywordStore(cfg configs.RAGConfig) (retrieval.KeywordStore, error) {
	switch cfg.KeywordBackend {
	case "elasticsearch", "es":
		esCfg := retrieval.ESConfig{
			Addresses:  cfg.Elasticsearch.Addresses,
			IndexName:  cfg.Elasticsearch.IndexName,
			Username:   cfg.Elasticsearch.Username,
			Password:   cfg.Elasticsearch.Password,
			Shards:     cfg.Elasticsearch.Shards,
			Replicas:   cfg.Elasticsearch.Replicas,
			BM25K1:     cfg.Elasticsearch.BM25K1,
			BM25B:      cfg.Elasticsearch.BM25B,
			AutoCreate: cfg.Elasticsearch.AutoCreate,
		}
		return retrieval.NewESKeywordStore(esCfg)

	case "memory", "":
		return retrieval.NewKeywordIndex(), nil

	default:
		return nil, fmt.Errorf("unknown keyword_backend: %q (supported: memory, elasticsearch)", cfg.KeywordBackend)
	}
}

// buildVectorStore creates the appropriate VectorStore based on config.
func buildVectorStore(cfg configs.RAGConfig, dim int) (index.VectorStore, error) {
	switch cfg.VectorBackend {
	case "milvus":
		mCfg := index.MilvusConfig{
			Address:              cfg.Milvus.Address,
			CollectionName:       cfg.Milvus.CollectionName,
			Dimension:            dim,
			MetricType:           cfg.Milvus.MetricType,
			IndexType:            cfg.Milvus.IndexType,
			Nlist:                cfg.Milvus.Nlist,
			Nprobe:               cfg.Milvus.Nprobe,
			EfConstruction:       cfg.Milvus.EfConstruction,
			Ef:                   cfg.Milvus.Ef,
			ConsistencyLevel:     cfg.Milvus.ConsistencyLevel,
			AutoCreateCollection: cfg.Milvus.AutoCreateCollection,
		}
		return index.NewMilvusVectorStore(mCfg, nil)

	case "memory", "":
		return index.NewMemoryVectorStore(dim), nil

	default:
		return nil, fmt.Errorf("unknown vector_backend: %q (supported: memory, milvus)", cfg.VectorBackend)
	}
}

// Ingest runs the full ingestion pipeline: load → chunk → embed → index.
func (e *Engine) Ingest(ctx context.Context, docPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(docPath)
	if err != nil {
		return fmt.Errorf("rag ingest: stat: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("rag ingest: use IngestDirectory for %s", docPath)
	}
	doc, err := loader.FileLoader(docPath)
	if err != nil {
		return fmt.Errorf("rag ingest: load: %w", err)
	}
	return e.ingestDocument(ctx, doc)
}

// IngestDirectory batch-ingests all supported documents under dirPath.
func (e *Engine) IngestDirectory(ctx context.Context, dirPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dl, err := loader.NewDirectoryLoader(dirPath)
	if err != nil {
		return fmt.Errorf("rag ingest dir: %w", err)
	}
	docs, err := dl.Load()
	if err != nil {
		return fmt.Errorf("rag ingest dir: walk: %w", err)
	}
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.ingestDocument(ctx, doc); err != nil {
			return fmt.Errorf("rag ingest dir: %s: %w", doc.Source, err)
		}
	}
	return nil
}

func (e *Engine) ingestDocument(ctx context.Context, doc loader.Document) error {
	chunks := e.chunker.ChunkDocument(doc.ID, doc.Content, doc.Metadata)
	var ids []string
	for _, ch := range chunks {
		vec, err := e.embedder.Embed(ctx, ch.Content)
		if err != nil {
			return fmt.Errorf("embed chunk %s: %w", ch.ID, err)
		}
		meta := map[string]interface{}{
			"content":  ch.Content,
			"doc_id":   ch.DocID,
			"chunk_id": ch.ID,
			"source":   doc.Source,
		}
		for k, v := range ch.Metadata {
			meta[k] = v
		}
		if err := e.indexer.Add(ctx, ch.ID, vec, meta); err != nil {
			return fmt.Errorf("index chunk %s: %w", ch.ID, err)
		}
		if err := e.keywordStore.Add(ctx, ch.ID, ch.Content); err != nil {
			return fmt.Errorf("keyword index chunk %s: %w", ch.ID, err)
		}
		ids = append(ids, ch.ID)
	}
	e.mu.Lock()
	e.docChunks[doc.Source] = append(e.docChunks[doc.Source], ids...)
	e.mu.Unlock()
	return nil
}

// Query runs retrieval: embed query → multi-channel retrieve → rerank → post-process → formatted context.
func (e *Engine) Query(ctx context.Context, question string, topK int) (string, error) {
	if topK <= 0 {
		topK = e.cfg.TopK
		if topK <= 0 {
			topK = 5
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	raw, err := e.retrieval.Retrieve(ctx, question, topK*2)
	if err != nil {
		return "", fmt.Errorf("rag query: retrieve: %w", err)
	}
	rerankK := e.cfg.RerankTopK
	if rerankK <= 0 {
		rerankK = topK
	}
	if rerankK < topK {
		rerankK = topK
	}
	ranked := e.reranker.Rerank(ctx, question, raw, rerankK)
	final, err := e.post.Run(ranked)
	if err != nil {
		return "", fmt.Errorf("rag query: post: %w", err)
	}
	if len(final) > topK {
		final = final[:topK]
	}
	return formatContext(final), nil
}

// QueryChunks returns ranked chunks after the same pipeline as Query (without string formatting).
func (e *Engine) QueryChunks(ctx context.Context, question string, topK int) ([]retrieval.ScoredChunk, error) {
	if topK <= 0 {
		topK = e.cfg.TopK
	}
	raw, err := e.retrieval.Retrieve(ctx, question, topK*2)
	if err != nil {
		return nil, err
	}
	rerankK := e.cfg.RerankTopK
	if rerankK < topK {
		rerankK = topK
	}
	ranked := e.reranker.Rerank(ctx, question, raw, rerankK)
	final, err := e.post.Run(ranked)
	if err != nil {
		return nil, err
	}
	if len(final) > topK {
		final = final[:topK]
	}
	return final, nil
}

func formatContext(chunks []retrieval.ScoredChunk) string {
	var b strings.Builder
	for i, ch := range chunks {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(fmt.Sprintf("[#%d score=%.4f]\n", i+1, ch.Score))
		b.WriteString(strings.TrimSpace(ch.Content))
	}
	return b.String()
}

// DeleteBySource removes all chunks previously ingested from the given file path.
func (e *Engine) DeleteBySource(ctx context.Context, sourcePath string) error {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return err
	}
	e.mu.Lock()
	ids := e.docChunks[abs]
	delete(e.docChunks, abs)
	e.mu.Unlock()
	for _, id := range ids {
		if err := e.keywordStore.Remove(ctx, id); err != nil {
			return fmt.Errorf("delete keyword chunk %s: %w", id, err)
		}
		if err := e.indexer.Delete(ctx, id); err != nil {
			return fmt.Errorf("delete chunk %s: %w", id, err)
		}
	}
	return nil
}

// BuildContextMessage wraps retrieved context and the user question in a types.Message for the agent.
func (e *Engine) BuildContextMessage(question, formattedContext string) types.Message {
	body := fmt.Sprintf("Use the following context when answering.\n\n%s\n\nQuestion:\n%s",
		strings.TrimSpace(formattedContext), strings.TrimSpace(question))
	return types.Message{
		Role:      types.RoleUser,
		Content:   body,
		Metadata:  map[string]interface{}{"source": "rag"},
		CreatedAt: time.Now().UTC(),
	}
}
