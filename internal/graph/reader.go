package graph

import (
	"context"
	"fmt"
	"strings"
)

// GraphReader executes knowledge graph queries with spoiler-free filtering.
type GraphReader struct {
	driver *Driver
}

func NewGraphReader(driver *Driver) *GraphReader {
	return &GraphReader{driver: driver}
}

func (r *GraphReader) IsEnabled() bool {
	return r.driver != nil && r.driver.IsEnabled()
}

// ---- Timeline queries ----

// RealmTimeline returns a character's realm breakthrough timeline.
type RealmEntry struct {
	Realm     string
	Level     int
	Chapter   int
	Age       int
}

func (r *GraphReader) RealmTimeline(ctx context.Context, novelID int64, charName string, maxChapter int) ([]RealmEntry, error) {
	if !r.IsEnabled() {
		return nil, nil
	}

	s := r.driver.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)

	result, err := s.Run(ctx, `
		MATCH (c:Character {novel_id: $novelId, name: $name})
		      -[b:BREAKTHROUGH_TO]->(r:Realm)
		WHERE b.at_chapter <= $maxChapter
		RETURN r.name AS realm, r.level AS level, b.at_chapter AS chapter, b.age AS age
		ORDER BY b.at_chapter
	`, map[string]any{
		"novelId":    novelID,
		"name":       charName,
		"maxChapter": maxChapter,
	})
	if err != nil {
		return nil, fmt.Errorf("realm timeline: %w", err)
	}

	var entries []RealmEntry
	for result.Next(ctx) {
		record := result.Record()
		realm, _ := record.Get("realm")
		level, _ := record.Get("level")
		chapter, _ := record.Get("chapter")
		age, _ := record.Get("age")

		entries = append(entries, RealmEntry{
			Realm:   toString(realm),
			Level:   toInt(level),
			Chapter: toInt(chapter),
			Age:     toInt(age),
		})
	}
	return entries, result.Err()
}

// ---- Relationship queries ----

// RelationEntry describes a relationship between two characters.
type RelationEntry struct {
	FromName     string
	ToName       string
	RelationType string
	SinceChapter int
	EndedChapter int // 0 if ongoing
}

// CharacterRelations returns all relationships for a character up to maxChapter.
func (r *GraphReader) CharacterRelations(ctx context.Context, novelID int64, charName string, maxChapter int) ([]RelationEntry, error) {
	if !r.IsEnabled() {
		return nil, nil
	}

	s := r.driver.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)

	result, err := s.Run(ctx, `
		MATCH (c:Character {novel_id: $novelId, name: $name})
		      -[r]-(other:Character)
		WHERE other.first_appearance_chapter <= $maxChapter
		  AND (r.since_chapter IS NULL OR r.since_chapter <= $maxChapter)
		  AND (r.ended_chapter IS NULL OR r.ended_chapter >= $maxChapter)
		  AND type(r) IN ['MASTER_OF', 'FRIEND_OF', 'ENEMY_OF', 'LOVE_INTEREST', 'BELONGS_TO']
		RETURN c.name AS fromName, other.name AS toName, type(r) AS relType,
		       r.since_chapter AS since, r.ended_chapter AS ended
		ORDER BY r.since_chapter
	`, map[string]any{
		"novelId":    novelID,
		"name":       charName,
		"maxChapter": maxChapter,
	})
	if err != nil {
		return nil, fmt.Errorf("character relations: %w", err)
	}

	var entries []RelationEntry
	for result.Next(ctx) {
		record := result.Record()
		from, _ := record.Get("fromName")
		to, _ := record.Get("toName")
		relType, _ := record.Get("relType")
		since, _ := record.Get("since")
		ended, _ := record.Get("ended")

		entries = append(entries, RelationEntry{
			FromName:     toString(from),
			ToName:       toString(to),
			RelationType: toString(relType),
			SinceChapter: toInt(since),
			EndedChapter: toInt(ended),
		})
	}
	return entries, result.Err()
}

// CharacterStatusTimeline returns a character's status across chapters.
type StatusEntry struct {
	Chapter int
	Status  string
	Age     int
}

func (r *GraphReader) CharacterStatusTimeline(ctx context.Context, novelID int64, charName string, maxChapter int) ([]StatusEntry, error) {
	if !r.IsEnabled() {
		return nil, nil
	}

	s := r.driver.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)

	result, err := s.Run(ctx, `
		MATCH (c:Character {novel_id: $novelId, name: $name})
		      -[a:APPEARS_IN]->(ch:Chapter)
		WHERE ch.chapter_number <= $maxChapter
		RETURN ch.chapter_number AS chapter, a.status AS status, a.age AS age
		ORDER BY ch.chapter_number
	`, map[string]any{
		"novelId":    novelID,
		"name":       charName,
		"maxChapter": maxChapter,
	})
	if err != nil {
		return nil, fmt.Errorf("status timeline: %w", err)
	}

	var entries []StatusEntry
	for result.Next(ctx) {
		record := result.Record()
		chapter, _ := record.Get("chapter")
		status, _ := record.Get("status")
		age, _ := record.Get("age")

		entries = append(entries, StatusEntry{
			Chapter: toInt(chapter),
			Status:  toString(status),
			Age:     toInt(age),
		})
	}
	return entries, result.Err()
}

// FormatTimeline formats a realm timeline for LLM context injection.
func FormatRealmTimeline(entries []RealmEntry, charName string, maxChapter int) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n=== %s 境界突破时间线（第1~%d章） ===\n", charName, maxChapter))
	for _, e := range entries {
		ageStr := ""
		if e.Age > 0 {
			ageStr = fmt.Sprintf("（%d岁）", e.Age)
		}
		sb.WriteString(fmt.Sprintf("- 第%d章: 突破至%s%s\n", e.Chapter, e.Realm, ageStr))
	}
	return sb.String()
}

// FormatRelations formats character relations for LLM context injection.
func FormatRelations(entries []RelationEntry, charName string, maxChapter int) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n=== %s 人际关系（截至第%d章） ===\n", charName, maxChapter))

	relLabel := map[string]string{
		"MASTER_OF":     "师父→",
		"FRIEND_OF":     "朋友↔",
		"ENEMY_OF":      "仇敌↔",
		"LOVE_INTEREST": "道侣♥",
		"BELONGS_TO":    "宗门∈",
	}

	for _, e := range entries {
		label := relLabel[e.RelationType]
		if label == "" {
			label = e.RelationType
		}

		endedStr := ""
		if e.EndedChapter > 0 && e.EndedChapter <= maxChapter {
			endedStr = fmt.Sprintf("（第%d章结束）", e.EndedChapter)
		}

		sb.WriteString(fmt.Sprintf("- %s %s %s（始于第%d章）%s\n",
			e.FromName, label, e.ToName, e.SinceChapter, endedStr))
	}
	return sb.String()
}

// ---- Type helpers ----

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toInt(v any) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
