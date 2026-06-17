package memory

import "github.com/cloudwego/eino/schema"

// MemoryStrategy decides which messages to keep from the full conversation history.
// Inspired by LangChain's memory types.
type MemoryStrategy interface {
	// ProcessMessages returns the messages that should be kept in context.
	// The input is the full message history (excluding system messages).
	ProcessMessages(msgs []*schema.Message) []*schema.Message
}

// BufferWindow keeps the last K exchanges (Human+AI pairs including tool calls/results).
// This is equivalent to LangChain's ConversationBufferWindowMemory.
type BufferWindow struct {
	K int // number of exchanges to keep (default 10)
}

// NewBufferWindow creates a BufferWindow strategy. A good default is K=10 (10 Q&A rounds).
func NewBufferWindow(k int) *BufferWindow {
	if k <= 0 {
		k = 10
	}
	return &BufferWindow{K: k}
}

// ProcessMessages keeps the last K user-assistant exchange pairs.
// An "exchange" = a UserMessage and everything after it until the next UserMessage.
// System messages should not be present in the input (they're injected at runtime).
func (b *BufferWindow) ProcessMessages(msgs []*schema.Message) []*schema.Message {
	if len(msgs) == 0 {
		return msgs
	}

	// Find all UserMessage positions
	var userPositions []int
	for i, m := range msgs {
		if m != nil && m.Role == schema.User {
			userPositions = append(userPositions, i)
		}
	}

	if len(userPositions) == 0 {
		return msgs
	}

	// Keep the last K exchanges
	startIdx := 0
	if len(userPositions) > b.K {
		startIdx = userPositions[len(userPositions)-b.K]
	}

	return msgs[startIdx:]
}
