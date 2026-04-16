package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MessageBus provides per-teammate JSONL inboxes on disk.
// Each teammate has a file at inboxDir/{name}.jsonl.
// Send appends; ReadInbox drains (returns all, then truncates).
type MessageBus struct {
	dir string
	mu  sync.Mutex // serializes concurrent writes to the same file
}

// NewMessageBus creates a bus backed by inboxDir.
func NewMessageBus(inboxDir string) (*MessageBus, error) {
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return nil, fmt.Errorf("bus: mkdir %s: %w", inboxDir, err)
	}
	return &MessageBus{dir: inboxDir}, nil
}

// Send appends a message to the recipient's inbox.
func (b *MessageBus) Send(sender, to, content, msgType string, extra map[string]interface{}) error {
	if err := ValidateMsgType(msgType); err != nil {
		return err
	}
	env := MessageEnvelope{
		Type:      msgType,
		From:      sender,
		Content:   content,
		Timestamp: float64(time.Now().UnixMilli()) / 1000.0,
	}
	if extra != nil {
		env.Payload = make(map[string]interface{}, len(extra))
		for k, v := range extra {
			if k == "request_id" {
				if s, ok := v.(string); ok {
					env.RequestID = s
					continue
				}
			}
			env.Payload[k] = v
		}
	}

	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("bus: marshal: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := filepath.Join(b.dir, to+".jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("bus: open %s: %w", p, err)
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("bus: write %s: %w", p, err)
	}
	return nil
}

// SendEnvelope appends a pre-built envelope to the recipient's inbox.
func (b *MessageBus) SendEnvelope(to string, env MessageEnvelope) error {
	if err := ValidateMsgType(env.Type); err != nil {
		return err
	}
	if env.Timestamp == 0 {
		env.Timestamp = float64(time.Now().UnixMilli()) / 1000.0
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("bus: marshal: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := filepath.Join(b.dir, to+".jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("bus: open %s: %w", p, err)
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadInbox drains and returns all messages from the named inbox.
// After reading, the file is truncated so messages are not re-delivered.
func (b *MessageBus) ReadInbox(name string) ([]MessageEnvelope, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	p := filepath.Join(b.dir, name+".jsonl")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("bus: read %s: %w", p, err)
	}

	// Truncate immediately so concurrent sends don't lose data
	// (they append; we only clear what we just read).
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		return nil, fmt.Errorf("bus: truncate %s: %w", p, err)
	}

	var msgs []MessageEnvelope
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var env MessageEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue // skip malformed lines
		}
		msgs = append(msgs, env)
	}
	return msgs, nil
}

// Broadcast sends a message to every name in the list except the sender.
func (b *MessageBus) Broadcast(sender, content string, names []string) (int, error) {
	count := 0
	for _, name := range names {
		if name == sender {
			continue
		}
		if err := b.Send(sender, name, content, MsgTypeBroadcast, nil); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
