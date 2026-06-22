package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"note-memory/internal/service/tools"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// GraphReader executes knowledge graph queries with spoiler-free filtering.
// Uses the simplified schema: 6 node types, 8 relation types.
type GraphReader struct {
	driver *Driver
}

func NewGraphReader(driver *Driver) *GraphReader {
	return &GraphReader{driver: driver}
}

func (r *GraphReader) IsEnabled() bool {
	return r.driver != nil && r.driver.IsEnabled()
}

// ── Realm timeline ───────────────────────────────────────────────────────────

type RealmEntry struct {
	Realm   string
	Chapter int
	Age     int
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
		MATCH (c:Character {novel_id: $nid, name: $name})
		      -[b:BREAKTHROUGH]->(r:Realm)
		WHERE b.chapter <= $max
		RETURN r.name AS realm, b.chapter AS chapter, b.age AS age
		ORDER BY b.chapter
	`, map[string]any{"nid": novelID, "name": charName, "max": maxChapter})
	if err != nil {
		return nil, fmt.Errorf("realm timeline: %w", err)
	}

	var entries []RealmEntry
	for result.Next(ctx) {
		rec := result.Record()
		entries = append(entries, RealmEntry{
			Realm:   getStr(rec, "realm"),
			Chapter: getInt(rec, "chapter"),
			Age:     getInt(rec, "age"),
		})
	}
	return entries, result.Err()
}

// ── Character relations ──────────────────────────────────────────────────────

type RelationEntry struct {
	FromName     string
	ToName       string
	RelationType string
	SinceChapter int
	EndedChapter int
	TriggerEvent string
	Description  string
}

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
		MATCH (c:Character {novel_id: $nid, name: $name})-[r]-(other:Character)
		WHERE other.first <= $max
		  AND (r.since IS NULL OR r.since <= $max)
		  AND (r.until IS NULL OR r.until >= $max)
		  AND type(r) IN ['MASTER_OF','FRIEND_OF','ENEMY_OF','LOVES','BELONGS_TO']
		RETURN c.name AS fromName, other.name AS toName, type(r) AS relType,
		       r.since AS since, r.until AS until,
		       r.trigger AS trigger, r.description AS description
		ORDER BY r.since
	`, map[string]any{"nid": novelID, "name": charName, "max": maxChapter})
	if err != nil {
		return nil, fmt.Errorf("character relations: %w", err)
	}

	var entries []RelationEntry
	for result.Next(ctx) {
		rec := result.Record()
		entries = append(entries, RelationEntry{
			FromName:     getStr(rec, "fromName"),
			ToName:       getStr(rec, "toName"),
			RelationType: getStr(rec, "relType"),
			SinceChapter: getInt(rec, "since"),
			EndedChapter: getInt(rec, "until"),
			TriggerEvent: getStr(rec, "trigger"),
			Description:  getStr(rec, "description"),
		})
	}
	return entries, result.Err()
}

// ── Technique timeline ───────────────────────────────────────────────────────

type TechniqueEntry struct {
	Technique    string
	Level        string
	Action       string
	Chapter      int
	Practitioner string
	Description  string
}

func (r *GraphReader) TechniqueTimeline(ctx context.Context, novelID int64, charName string, maxChapter int) ([]TechniqueEntry, error) {
	if !r.IsEnabled() {
		return nil, nil
	}
	s := r.driver.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)

	// level is a property on the LEARNS edge — no TechniqueLevel node
	result, err := s.Run(ctx, `
		MATCH (c:Character {novel_id: $nid, name: $name})
		      -[l:LEARNS]->(t:Technique)
		WHERE l.chapter <= $max
		RETURN t.name AS technique, l.level AS level, l.action AS action,
		       l.chapter AS chapter, l.description AS description
		ORDER BY l.chapter
	`, map[string]any{"nid": novelID, "name": charName, "max": maxChapter})
	if err != nil {
		return nil, fmt.Errorf("technique timeline: %w", err)
	}

	var entries []TechniqueEntry
	for result.Next(ctx) {
		rec := result.Record()
		entries = append(entries, TechniqueEntry{
			Technique:    getStr(rec, "technique"),
			Level:        getStr(rec, "level"),
			Action:       getStr(rec, "action"),
			Chapter:      getInt(rec, "chapter"),
			Practitioner: charName,
			Description:  getStr(rec, "description"),
		})
	}
	return entries, result.Err()
}

// AllTechniques returns all known techniques across all characters up to maxChapter.
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
		MATCH (c:Character)-[l:LEARNS]->(t:Technique {novel_id: $nid})
		WHERE l.chapter <= $max
		RETURN t.name AS technique, l.level AS level, l.action AS action,
		       l.chapter AS chapter, c.name AS practitioner, l.description AS description
		ORDER BY l.chapter
	`, map[string]any{"nid": novelID, "max": maxChapter})
	if err != nil {
		return nil, fmt.Errorf("all techniques: %w", err)
	}

	var entries []TechniqueEntry
	for result.Next(ctx) {
		rec := result.Record()
		entries = append(entries, TechniqueEntry{
			Technique:    getStr(rec, "technique"),
			Level:        getStr(rec, "level"),
			Action:       getStr(rec, "action"),
			Chapter:      getInt(rec, "chapter"),
			Practitioner: getStr(rec, "practitioner"),
			Description:  getStr(rec, "description"),
		})
	}
	return entries, result.Err()
}

// ── Character events ─────────────────────────────────────────────────────────

type EventEntry struct {
	Title   string
	Chapter int
	Summary string
	Role    string
}

func (r *GraphReader) CharacterEvents(ctx context.Context, novelID int64, charName string, maxChapter int) ([]EventEntry, error) {
	if !r.IsEnabled() {
		return nil, nil
	}
	s := r.driver.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)

	result, err := s.Run(ctx, `
		MATCH (c:Character {novel_id: $nid, name: $name})
		      -[inv:INVOLVED_IN]->(e:Event)
		WHERE inv.chapter <= $max
		RETURN e.title AS title, inv.chapter AS chapter,
		       e.summary AS summary, inv.role AS role
		ORDER BY inv.chapter DESC
		LIMIT 20
	`, map[string]any{"nid": novelID, "name": charName, "max": maxChapter})
	if err != nil {
		return nil, fmt.Errorf("character events: %w", err)
	}

	var entries []EventEntry
	for result.Next(ctx) {
		rec := result.Record()
		entries = append(entries, EventEntry{
			Title:   getStr(rec, "title"),
			Chapter: getInt(rec, "chapter"),
			Summary: getStr(rec, "summary"),
			Role:    getStr(rec, "role"),
		})
	}
	return entries, result.Err()
}

// ── Tool factories for ADK agents ────────────────────────────────────────────

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
				Chapter: e.Chapter, Practitioner: e.Practitioner, Description: e.Description,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

func (r *GraphReader) EventsTool() func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
	return func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error) {
		if r == nil || !r.IsEnabled() {
			return `[]`, nil
		}
		if charName == "" {
			return `[]`, nil
		}
		entries, err := r.CharacterEvents(ctx, novelID, charName, maxChapter)
		if err != nil {
			return "", err
		}
		var out []tools.EventEntry
		for _, e := range entries {
			out = append(out, tools.EventEntry{
				Title: e.Title, Chapter: e.Chapter,
				Summary: e.Summary, Role: e.Role,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

// ── Record helpers ────────────────────────────────────────────────────────────

func getStr(rec *neo4j.Record, key string) string {
	// nolint:staticcheck // Record.Get returns (any, bool), we only need the value
	v, _ := rec.Get(key)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func getInt(rec *neo4j.Record, key string) int {
	v, _ := rec.Get(key)
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
