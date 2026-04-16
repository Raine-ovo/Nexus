package reflection

import (
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/pkg/types"
)

// toMessages wraps a single user string into the message slice expected by ChatModel.
func toMessages(content string) []types.Message {
	return []types.Message{{
		ID:        uuid.NewString(),
		Role:      types.RoleUser,
		Content:   content,
		CreatedAt: time.Now(),
	}}
}
