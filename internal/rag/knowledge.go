package rag

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/internal/rag/loader"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// IngestStatus describes lifecycle state of a document in a knowledge base.
type IngestStatus string

const (
	StatusPending IngestStatus = "pending"
	StatusIndexed IngestStatus = "indexed"
	StatusFailed  IngestStatus = "failed"
	StatusRemoved IngestStatus = "removed"
)

// DocumentRecord tracks one file under a knowledge base.
type DocumentRecord struct {
	Path       string
	Status     IngestStatus
	LastIngest time.Time
	Error      string
}

// KnowledgeBase is a logical collection of documents rooted at a directory.
type KnowledgeBase struct {
	mu        sync.RWMutex
	ID        string
	RootPath  string
	Documents map[string]*DocumentRecord
	createdAt time.Time
}

// Manager provides CRUD for knowledge bases and ties them to a RAG Engine.
type Manager struct {
	mu     sync.RWMutex
	bases  map[string]*KnowledgeBase
	engine *Engine
}

// NewKnowledgeManager constructs a manager; engine may be nil for metadata-only use.
func NewKnowledgeManager(engine *Engine) *Manager {
	return &Manager{
		bases:  make(map[string]*KnowledgeBase),
		engine: engine,
	}
}

// CreateKnowledgeBase registers a new base with a unique id and root directory.
func (m *Manager) CreateKnowledgeBase(ctx context.Context, id, rootPath string) (*KnowledgeBase, error) {
	_ = ctx
	if rootPath == "" {
		return nil, fmt.Errorf("knowledge: empty root path")
	}
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("knowledge: resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("knowledge: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("knowledge: root is not a directory: %s", abs)
	}
	if id == "" {
		id = uuid.NewString()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.bases[id]; exists {
		return nil, fmt.Errorf("knowledge: base %q already exists", id)
	}
	kb := &KnowledgeBase{
		ID:        id,
		RootPath:  abs,
		Documents: make(map[string]*DocumentRecord),
		createdAt: time.Now().UTC(),
	}
	m.bases[id] = kb
	return kb, nil
}

// GetKnowledgeBase returns a base by id.
func (m *Manager) GetKnowledgeBase(id string) (*KnowledgeBase, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	kb, ok := m.bases[id]
	return kb, ok
}

// AddDocument records a document and optionally ingests it through the engine.
// relPath is relative to the knowledge base root (or absolute path inside root).
func (m *Manager) AddDocument(ctx context.Context, baseID, relPath string, ingest bool) error {
	kb, ok := m.GetKnowledgeBase(baseID)
	if !ok {
		return fmt.Errorf("knowledge: unknown base %q", baseID)
	}
	full, err := utils.SafePath(kb.RootPath, relPath)
	if err != nil {
		return fmt.Errorf("knowledge: path: %w", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("knowledge: stat document: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("knowledge: document path is a directory: %s", full)
	}

	kb.mu.Lock()
	kb.Documents[full] = &DocumentRecord{
		Path:   full,
		Status: StatusPending,
	}
	kb.mu.Unlock()

	if !ingest || m.engine == nil {
		return nil
	}
	if err := m.engine.Ingest(ctx, full); err != nil {
		kb.mu.Lock()
		if rec, ok := kb.Documents[full]; ok {
			rec.Status = StatusFailed
			rec.Error = err.Error()
			rec.LastIngest = time.Now().UTC()
		}
		kb.mu.Unlock()
		return fmt.Errorf("knowledge: ingest: %w", err)
	}
	kb.mu.Lock()
	if rec, ok := kb.Documents[full]; ok {
		rec.Status = StatusIndexed
		rec.Error = ""
		rec.LastIngest = time.Now().UTC()
	}
	kb.mu.Unlock()
	return nil
}

// RemoveDocument removes metadata and deletes vectors for the file if engine is set.
func (m *Manager) RemoveDocument(ctx context.Context, baseID, relPath string) error {
	kb, ok := m.GetKnowledgeBase(baseID)
	if !ok {
		return fmt.Errorf("knowledge: unknown base %q", baseID)
	}
	full, err := utils.SafePath(kb.RootPath, relPath)
	if err != nil {
		return err
	}
	if m.engine != nil {
		if err := m.engine.DeleteBySource(ctx, full); err != nil {
			return fmt.Errorf("knowledge: delete vectors: %w", err)
		}
	}
	kb.mu.Lock()
	if rec, ok := kb.Documents[full]; ok {
		rec.Status = StatusRemoved
		rec.LastIngest = time.Now().UTC()
		delete(kb.Documents, full)
	}
	kb.mu.Unlock()
	return nil
}

// ListDocuments returns stable-sorted document records for a base.
func (m *Manager) ListDocuments(baseID string) ([]DocumentRecord, error) {
	kb, ok := m.GetKnowledgeBase(baseID)
	if !ok {
		return nil, fmt.Errorf("knowledge: unknown base %q", baseID)
	}
	kb.mu.RLock()
	defer kb.mu.RUnlock()
	out := make([]DocumentRecord, 0, len(kb.Documents))
	for _, rec := range kb.Documents {
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// IngestAllPending runs Ingest on every document marked pending (or all files if force).
func (m *Manager) IngestAllPending(ctx context.Context, baseID string, force bool) error {
	kb, ok := m.GetKnowledgeBase(baseID)
	if !ok {
		return fmt.Errorf("knowledge: unknown base %q", baseID)
	}
	if m.engine == nil {
		return fmt.Errorf("knowledge: no engine configured")
	}
	kb.mu.RLock()
	paths := make([]string, 0, len(kb.Documents))
	for p, rec := range kb.Documents {
		if force || rec.Status == StatusPending || rec.Status == StatusFailed {
			paths = append(paths, p)
		}
	}
	kb.mu.RUnlock()
	sort.Strings(paths)
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.engine.Ingest(ctx, p); err != nil {
			kb.mu.Lock()
			if rec, ok := kb.Documents[p]; ok {
				rec.Status = StatusFailed
				rec.Error = err.Error()
				rec.LastIngest = time.Now().UTC()
			}
			kb.mu.Unlock()
			return fmt.Errorf("knowledge: ingest %s: %w", p, err)
		}
		kb.mu.Lock()
		if rec, ok := kb.Documents[p]; ok {
			rec.Status = StatusIndexed
			rec.Error = ""
			rec.LastIngest = time.Now().UTC()
		}
		kb.mu.Unlock()
	}
	return nil
}

// IngestKnowledgeBaseDirectory loads every supported file under the base root.
func (m *Manager) IngestKnowledgeBaseDirectory(ctx context.Context, baseID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	kb, ok := m.GetKnowledgeBase(baseID)
	if !ok {
		return fmt.Errorf("knowledge: unknown base %q", baseID)
	}
	if m.engine == nil {
		return fmt.Errorf("knowledge: no engine configured")
	}
	if err := m.engine.IngestDirectory(ctx, kb.RootPath); err != nil {
		return err
	}
	// Mark all files under root as indexed in metadata (best-effort scan)
	return filepath.WalkDir(kb.RootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != kb.RootPath {
				return filepath.SkipDir
			}
			return nil
		}
		if !loaderIsSupported(path) {
			return nil
		}
		kb.mu.Lock()
		kb.Documents[path] = &DocumentRecord{
			Path:       path,
			Status:     StatusIndexed,
			LastIngest: time.Now().UTC(),
		}
		kb.mu.Unlock()
		return nil
	})
}

// ToStatusMessage builds a user-role message summarizing document ingestion state for agent or audit surfaces.
func (r DocumentRecord) ToStatusMessage(baseID string) types.Message {
	body := fmt.Sprintf("Knowledge document %s: status=%s", r.Path, r.Status)
	if r.Error != "" {
		body += fmt.Sprintf(` error=%q`, r.Error)
	}
	md := map[string]interface{}{
		"source":  "knowledge_base",
		"base_id": baseID,
		"path":    r.Path,
		"status":  string(r.Status),
	}
	return types.Message{
		Role:      types.RoleUser,
		Content:   body,
		Metadata:  md,
		CreatedAt: time.Now().UTC(),
	}
}

func loaderIsSupported(path string) bool {
	ext := filepath.Ext(path)
	return loader.IsSupportedExt(ext)
}
