package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteJSON atomically writes a value as JSON to a file.
func WriteJSON(path string, v interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// ReadJSON reads a JSON file into the given pointer.
func ReadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return nil
}

// MustMarshal marshals to JSON or panics.
func MustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("json marshal: %v", err))
	}
	return data
}

// MapToStruct converts a map[string]interface{} to a struct via JSON round-trip.
func MapToStruct(m map[string]interface{}, out interface{}) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// GetString safely extracts a string from a map.
func GetString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt safely extracts an int from a map.
func GetInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
	}
	return 0
}

// GetBool safely extracts a bool from a map.
func GetBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
