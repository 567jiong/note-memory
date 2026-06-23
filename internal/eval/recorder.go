package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// recorder implements Recorder. Thread-safe.
type recorder struct {
	mu sync.Mutex

	record  *RunRecord
	stepNum int

	// pending maps toolName → the ToolCallRecord that is waiting for its result.
	// Since multiple tools can be called in parallel within one step,
	// we use a queue per tool name.
	pending []*pendingCall
}

type pendingCall struct {
	ToolCall ToolCallRecord
	Since    time.Time
}

// NewRecorder creates a new Recorder for capturing agent execution data.
func NewRecorder(question, novelTitle string, maxChapter int) Recorder {
	return &recorder{
		record: &RunRecord{
			Question:   question,
			NovelTitle: novelTitle,
			MaxChapter: maxChapter,
			StartedAt:  time.Now(),
		},
	}
}

func (r *recorder) OnThinking(step int, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Thinking = append(r.record.Thinking, strings.TrimSpace(content))
}

func (r *recorder) OnToolCall(step int, toolName string, args string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stepNum = step
	r.pending = append(r.pending, &pendingCall{
		ToolCall: ToolCallRecord{
			Step:      step,
			ToolName:  toolName,
			Arguments: args,
		},
		Since: time.Now(),
	})
}

func (r *recorder) OnToolResult(toolName string, result string, toolErr string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find the first pending call with a matching tool name.
	var idx int = -1
	for i, p := range r.pending {
		if p.ToolCall.ToolName == toolName {
			idx = i
			break
		}
	}

	var tc ToolCallRecord
	if idx >= 0 {
		tc = r.pending[idx].ToolCall
		tc.Duration = time.Since(r.pending[idx].Since)
		// Remove from pending
		r.pending = append(r.pending[:idx], r.pending[idx+1:]...)
	} else {
		// Orphan result — still record it.
		tc = ToolCallRecord{
			ToolName: toolName,
			Step:     r.stepNum,
		}
	}

	tc.Result = truncate(result, 1000)
	tc.Error = toolErr
	r.record.ToolCalls = append(r.record.ToolCalls, tc)
}

func (r *recorder) OnFinalAnswer(answer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.FinalAnswer = answer
	r.record.Duration = time.Since(r.record.StartedAt)
}

func (r *recorder) OnError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Error = err.Error()
	r.record.Duration = time.Since(r.record.StartedAt)
}

func (r *recorder) Record() *RunRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *r.record
	cp.ToolCalls = make([]ToolCallRecord, len(r.record.ToolCalls))
	copy(cp.ToolCalls, r.record.ToolCalls)
	cp.Thinking = make([]string, len(r.record.Thinking))
	copy(cp.Thinking, r.record.Thinking)
	return &cp
}

// SaveRunRecord writes a RunRecord to a JSON file.
func SaveRunRecord(record *RunRecord, path string) error {
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run record: %w", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("write run record: %w", err)
	}
	return nil
}


func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}
