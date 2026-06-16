package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"note-memory/internal/service/tools"
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

// RealmEntry is a single realm breakthrough record.
type RealmEntry struct {
	Realm   string
	Chapter int
	Age     int
}

// RealmTimeline returns a character's realm breakthrough timeline.
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
		RETURN r.name AS realm, b.at_chapter AS chapter, b.age AS age
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
		chapter, _ := record.Get("chapter")
		age, _ := record.Get("age")

		entries = append(entries, RealmEntry{
			Realm:   toString(realm),
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
	TriggerEvent string
	Description  string
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
		       r.since_chapter AS since, r.ended_chapter AS ended,
		       r.trigger_event AS triggerEvent, r.description AS description
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
		triggerEvent, _ := record.Get("triggerEvent")
		description, _ := record.Get("description")

		entries = append(entries, RelationEntry{
			FromName:     toString(from),
			ToName:       toString(to),
			RelationType: toString(relType),
			SinceChapter: toInt(since),
			EndedChapter: toInt(ended),
			TriggerEvent: toString(triggerEvent),
			Description:  toString(description),
		})
	}
	return entries, result.Err()
}

// ---- Technique queries ----

// TechniqueEntry is a single technique acquisition record.
type TechniqueEntry struct {
	Technique   string
	Level       string
	Action      string
	Chapter     int
	Practitioner string
	Description string
}

// TechniqueTimeline returns a character's technique acquisition timeline.
func (r *GraphReader) TechniqueTimeline(ctx context.Context, novelID int64, charName string, maxChapter int) ([]TechniqueEntry, error) {
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
		      -[l:LEARNS]->(t:Technique)
		WHERE l.at_chapter <= $maxChapter
		OPTIONAL MATCH (c)-[lr:LEARNS_LEVEL]->(tl:TechniqueLevel {novel_id: $novelId})
		WHERE lr.at_chapter <= $maxChapter AND tl.technique_name = t.name
		RETURN t.name AS technique, tl.level_name AS level, l.action AS action,
		       l.at_chapter AS chapter, l.description AS description
		ORDER BY l.at_chapter
	`, map[string]any{
		"novelId":    novelID,
		"name":       charName,
		"maxChapter": maxChapter,
	})
	if err != nil {
		return nil, fmt.Errorf("technique timeline: %w", err)
	}

	var entries []TechniqueEntry
	for result.Next(ctx) {
		record := result.Record()
		tech, _ := record.Get("technique")
		level, _ := record.Get("level")
		action, _ := record.Get("action")
		chapter, _ := record.Get("chapter")
		description, _ := record.Get("description")

		entries = append(entries, TechniqueEntry{
			Technique:    toString(tech),
			Level:        toString(level),
			Action:       toString(action),
			Chapter:      toInt(chapter),
			Practitioner: charName,
			Description:  toString(description),
		})
	}
	return entries, result.Err()
}

// AllTechniques returns all known techniques up to maxChapter.
func (r *GraphReader) AllTechniques(ctx context.Context, novelID int64, maxChapter int) ([]TechniqueEntry, error) {
	if !r.IsEnabled() {
		return nil, nil
	}

	s := r.driver.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)

	result, err := s.Run(ctx, `
		MATCH (c:Character)-[l:LEARNS]->(t:Technique {novel_id: $novelId})
		WHERE l.at_chapter <= $maxChapter
		OPTIONAL MATCH (c)-[lr:LEARNS_LEVEL]->(tl:TechniqueLevel {novel_id: $novelId})
		WHERE lr.at_chapter <= $maxChapter AND tl.technique_name = t.name
		RETURN t.name AS technique, tl.level_name AS level, l.action AS action,
		       l.at_chapter AS chapter, c.name AS practitioner, l.description AS description
		ORDER BY l.at_chapter
	`, map[string]any{
		"novelId":    novelID,
		"maxChapter": maxChapter,
	})
	if err != nil {
		return nil, fmt.Errorf("all techniques: %w", err)
	}

	var entries []TechniqueEntry
	for result.Next(ctx) {
		record := result.Record()
		tech, _ := record.Get("technique")
		level, _ := record.Get("level")
		action, _ := record.Get("action")
		chapter, _ := record.Get("chapter")
		practitioner, _ := record.Get("practitioner")
		description, _ := record.Get("description")

		entries = append(entries, TechniqueEntry{
			Technique:    toString(tech),
			Level:        toString(level),
			Action:       toString(action),
			Chapter:      toInt(chapter),
			Practitioner: toString(practitioner),
			Description:  toString(description),
		})
	}
	return entries, result.Err()
}

// ---- Tool factories for ADK agents ----

// TimelineTool returns a closure matching tools.Deps.TimelineFunc.
func (r *GraphReader) TimelineTool() func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
	return func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
		if r == nil || !r.IsEnabled() {
			return `[]`, nil
		}
		entries, err := r.RealmTimeline(ctx, novelID, charName, maxChapter)
		if err != nil {
			return "", err
		}
		var out []tools.TimelineEntry
		for _, e := range entries {
			out = append(out, tools.TimelineEntry{Realm: e.Realm, Chapter: e.Chapter, Age: e.Age})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

// RelationsTool returns a closure matching tools.Deps.RelationsFunc.
func (r *GraphReader) RelationsTool() func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
	return func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
		if r == nil || !r.IsEnabled() {
			return `[]`, nil
		}
		entries, err := r.CharacterRelations(ctx, novelID, charName, maxChapter)
		if err != nil {
			return "", err
		}
		var out []tools.RelationEntry
		for _, e := range entries {
			out = append(out, tools.RelationEntry{
				From: e.FromName, To: e.ToName, RelType: e.RelationType,
				Since: e.SinceChapter, Ended: e.EndedChapter,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

// TechniqueTool returns a closure matching tools.Deps.TechniqueFunc.
func (r *GraphReader) TechniqueTool() func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
	return func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
		if r == nil || !r.IsEnabled() {
			return `[]`, nil
		}
		entries, err := r.TechniqueTimeline(ctx, novelID, charName, maxChapter)
		if err != nil {
			return "", err
		}
		var out []tools.TechniqueEntry
		for _, e := range entries {
			out = append(out, tools.TechniqueEntry{
				Technique: e.Technique, Level: e.Level, Action: e.Action,
				Chapter: e.Chapter, Description: e.Description,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

// AllTechniquesTool returns a closure matching tools.Deps.AllTechniquesFunc.
func (r *GraphReader) AllTechniquesTool() func(ctx context.Context, novelID int64, maxChapter int) (string, error) {
	return func(ctx context.Context, novelID int64, maxChapter int) (string, error) {
		if r == nil || !r.IsEnabled() {
			return `[]`, nil
		}
		entries, err := r.AllTechniques(ctx, novelID, maxChapter)
		if err != nil {
			return "", err
		}
		var out []tools.TechniqueEntry
		for _, e := range entries {
			out = append(out, tools.TechniqueEntry{
				Technique: e.Technique, Level: e.Level, Action: e.Action,
				Chapter: e.Chapter, Description: e.Description,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
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
	case string:
		var val int
		fmt.Sscanf(n, "%d", &val)
		return val
	}
	return 0
}
