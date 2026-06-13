package graph

import (
	"context"
	"strings"
)

// QueryClass represents the type of user question.
type QueryClass int

const (
	QueryFact     QueryClass = iota // "掌天瓶是什么" → PG Chunk search
	QueryTimeline                   // "境界突破年龄" → Neo4j
	QueryRelation                   // "韩立的仇敌" → Neo4j
	QueryMixed                      // Both → merge
)

// QueryContext holds the enriched context from graph queries.
type QueryContext struct {
	RealmTimeline string // formatted realm breakthrough timeline
	Relations     string // formatted character relationships
	StatusChanges string // formatted status timeline
}

// RouteQuery classifies a question and enriches it with graph data if applicable.
// Returns the enriched context string to inject into the LLM prompt.
func RouteQuery(ctx context.Context, reader *GraphReader, question string, novelID int64, charName string, maxChapter int) (*QueryContext, QueryClass) {
	class := classify(question)
	qc := &QueryContext{}

	if !reader.IsEnabled() {
		return qc, class
	}

	switch class {
	case QueryTimeline:
		if timeline, err := reader.RealmTimeline(ctx, novelID, charName, maxChapter); err == nil {
			qc.RealmTimeline = FormatRealmTimeline(timeline, charName, maxChapter)
		}
		if statuses, err := reader.CharacterStatusTimeline(ctx, novelID, charName, maxChapter); err == nil {
			qc.StatusChanges = formatStatusTimeline(statuses, charName, maxChapter)
		}
	case QueryRelation:
		if relations, err := reader.CharacterRelations(ctx, novelID, charName, maxChapter); err == nil {
			qc.Relations = FormatRelations(relations, charName, maxChapter)
		}
	case QueryMixed:
		if timeline, err := reader.RealmTimeline(ctx, novelID, charName, maxChapter); err == nil {
			qc.RealmTimeline = FormatRealmTimeline(timeline, charName, maxChapter)
		}
		if relations, err := reader.CharacterRelations(ctx, novelID, charName, maxChapter); err == nil {
			qc.Relations = FormatRelations(relations, charName, maxChapter)
		}
	}
	return qc, class
}

// classify determines the query type based on keyword matching.
func classify(question string) QueryClass {
	q := strings.ToLower(question)

	timelineKW := []string{"境界", "突破", "年龄", "多少岁", "时间线", "修炼历程", "升级", "什么修为", "什么境界", "什么时候"}
	relationKW := []string{"仇敌", "仇人", "敌人", "师徒", "师父", "徒弟", "道侣", "朋友", "宗门", "认识", "关系", "恩人", "联盟", "敌对"}

	timeline := false
	relation := false

	for _, kw := range timelineKW {
		if strings.Contains(q, kw) {
			timeline = true
			break
		}
	}
	for _, kw := range relationKW {
		if strings.Contains(q, kw) {
			relation = true
			break
		}
	}

	if timeline && relation {
		return QueryMixed
	}
	if timeline {
		return QueryTimeline
	}
	if relation {
		return QueryRelation
	}
	return QueryFact
}

func formatStatusTimeline(entries []StatusEntry, charName string, maxChapter int) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n=== " + charName + " 状态变化时间线（第1~" + itoa(maxChapter) + "章） ===\n")
	for _, e := range entries {
		ageStr := ""
		if e.Age > 0 {
			ageStr = "（" + itoa(e.Age) + "岁）"
		}
		sb.WriteString("- 第" + itoa(e.Chapter) + "章" + ageStr + ": " + e.Status + "\n")
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
