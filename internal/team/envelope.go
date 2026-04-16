package team

import "fmt"

// Message type constants used in the team inbox system.
const (
	MsgTypeMessage              = "message"
	MsgTypeBroadcast            = "broadcast"
	MsgTypeShutdownRequest      = "shutdown_request"
	MsgTypeShutdownResponse     = "shutdown_response"
	MsgTypePlanApproval         = "plan_approval"
	MsgTypePlanApprovalResponse = "plan_approval_response"
)

var validMsgTypes = map[string]bool{
	MsgTypeMessage:              true,
	MsgTypeBroadcast:            true,
	MsgTypeShutdownRequest:      true,
	MsgTypeShutdownResponse:     true,
	MsgTypePlanApproval:         true,
	MsgTypePlanApprovalResponse: true,
}

// MessageEnvelope wraps a message with metadata for the inbox system.
// Protocol messages carry a RequestID; plain messages leave it empty.
type MessageEnvelope struct {
	Type      string                 `json:"type"`
	From      string                 `json:"from"`
	Content   string                 `json:"content"`
	Timestamp float64                `json:"timestamp"`
	RequestID string                 `json:"request_id,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// ValidateMsgType returns an error if msgType is not recognized.
func ValidateMsgType(msgType string) error {
	if !validMsgTypes[msgType] {
		return fmt.Errorf("envelope: invalid message type %q", msgType)
	}
	return nil
}

// IsProtocol returns true if the envelope carries a structured protocol request or response.
func (e *MessageEnvelope) IsProtocol() bool {
	switch e.Type {
	case MsgTypeShutdownRequest, MsgTypeShutdownResponse,
		MsgTypePlanApproval, MsgTypePlanApprovalResponse:
		return true
	}
	return false
}
