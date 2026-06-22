package graph

import (
	"context"
	"fmt"
	"log"
	"note-memory/internal/model"
	"regexp"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// GraphWriter syncs AI-extracted chapter data into the Neo4j knowledge graph.
//
// Node types (6):   Novel, Chapter, Character, Realm, Event, Technique
// Relation types (8): HAS_CHAPTER, APPEARS_IN, BREAKTHROUGH,
//                      MASTER_OF, FRIEND_OF, ENEMY_OF, LOVES, BELONGS_TO,
//                      LEARNS, INVOLVED_IN, OCCURS_IN
type GraphWriter struct {
	driver *Driver
}

func NewGraphWriter(driver *Driver) *GraphWriter {
	return &GraphWriter{driver: driver}
}

func (w *GraphWriter) IsEnabled() bool {
	return w.driver != nil && w.driver.IsEnabled()
}

// SyncChapter writes chapter entities, relationships, and techniques to Neo4j.
// Errors on individual items are logged and skipped (best-effort per item).
func (w *GraphWriter) SyncChapter(
	ctx context.Context,
	novel *model.Novel,
	ch *model.Chapter,
	chars []model.CharacterInfo,
	events []model.EventInfo,
	relations []model.CharacterRelation,
	techniques []model.TechniqueInfo,
) error {
	if !w.IsEnabled() {
		return nil
	}

	s := w.driver.Session(ctx)
	if s == nil {
		return nil
	}
	defer s.Close(ctx)

	// 1. Novel + Chapter
	if _, err := s.Run(ctx, `
		MERGE (n:Novel {id: $id})  ON CREATE SET n.title = $title, n.author = $author
		MERGE (c:Chapter {id: $chId}) ON CREATE SET c.novel_id = $id, c.number = $num, c.title = $chTitle
		MERGE (n)-[:HAS_CHAPTER]->(c)
	`, map[string]any{
		"id":       novel.ID,
		"title":    novel.Title,
		"author":   novel.Author,
		"chId":     ch.ID,
		"num":      ch.ChapterNumber,
		"chTitle":  ch.Title,
	}); err != nil {
		return fmt.Errorf("sync novel+chapter: %w", err)
	}

	// 2. Characters
	nChar := 0
	for _, char := range chars {
		if char.Name == "" || isNoisyName(char.Name) {
			continue
		}
		if err := w.syncCharacter(ctx, s, novel.ID, ch.ChapterNumber, ch.ID, char); err != nil {
			log.Printf("[graph] ⚠️ ch%d char[%s]: %v", ch.ChapterNumber, char.Name, err)
			continue
		}
		nChar++
	}

	// 3. Events
	nEvt := 0
	for _, evt := range events {
		if evt.Title == "" {
			continue
		}
		if err := w.syncEvent(ctx, s, novel.ID, ch.ChapterNumber, ch.ID, evt); err != nil {
			log.Printf("[graph] ⚠️ ch%d event[%s]: %v", ch.ChapterNumber, evt.Title, err)
			continue
		}
		nEvt++
	}

	// 4. Relations (BELONGS_TO is now a real edge)
	nRel := 0
	for _, rel := range relations {
		if rel.FromName == "" || rel.ToName == "" || rel.RelationType == "" {
			continue
		}
		if isNoisyName(rel.FromName) || isNoisyName(rel.ToName) {
			continue
		}
		if err := w.syncRelation(ctx, s, novel.ID, ch.ChapterNumber, rel); err != nil {
			log.Printf("[graph] ⚠️ ch%d rel[%s -%s-> %s]: %v",
				ch.ChapterNumber, rel.FromName, rel.RelationType, rel.ToName, err)
			continue
		}
		nRel++
	}

	// 5. Techniques (level is now a property on LEARNS edge, no TechniqueLevel node)
	nTech := 0
	for _, tech := range techniques {
		if tech.Name == "" || tech.Practitioner == "" {
			continue
		}
		if err := w.syncTechnique(ctx, s, novel.ID, ch.ChapterNumber, tech); err != nil {
			log.Printf("[graph] ⚠️ ch%d tech[%s]: %v", ch.ChapterNumber, tech.Name, err)
			continue
		}
		nTech++
	}

	log.Printf("[graph] ✅ ch%d: %d char %d event %d rel %d tech",
		ch.ChapterNumber, nChar, nEvt, nRel, nTech)
	return nil
}

// ── syncCharacter ────────────────────────────────────────────────────────────

func (w *GraphWriter) syncCharacter(ctx context.Context, s neo4j.Session,
	novelID int64, chapterNum int, chapterID int64, char model.CharacterInfo) error {

	charType := char.Type
	if charType == "" {
		charType = "配角"
	}

	_, err := s.Run(ctx, `
		MERGE (c:Character {novel_id: $nid, name: $name})
		  ON CREATE SET c.type = $type, c.first = $ch, c.last = $ch
		  ON MATCH  SET c.type = CASE WHEN c.type = '路人' AND $type <> '路人' THEN $type ELSE c.type END,
		               c.last = $ch
		WITH c
		MATCH (ch:Chapter {id: $chId})
		MERGE (c)-[r:APPEARS_IN {chapter: $ch}]->(ch)
		  ON CREATE SET r.status = $status, r.age = $age
	`, map[string]any{
		"nid":    novelID,
		"name":   char.Name,
		"type":   charType,
		"ch":     chapterNum,
		"chId":   chapterID,
		"status": char.Status,
		"age":    extractAge(char.Status),
	})
	if err != nil {
		return fmt.Errorf("merge character: %w", err)
	}

	// Realm breakthrough
	if char.Realm != "" {
		if err := w.syncBreakthrough(ctx, s, novelID, char.Name, chapterNum, char.Realm, extractAge(char.Status)); err != nil {
			log.Printf("[graph] ⚠️ ch%d realm[%s→%s]: %v", chapterNum, char.Name, char.Realm, err)
		}
	}
	return nil
}

// ── syncBreakthrough ─────────────────────────────────────────────────────────

func (w *GraphWriter) syncBreakthrough(ctx context.Context, s neo4j.Session,
	novelID int64, charName string, chapterNum int, realm string, age int) error {

	_, err := s.Run(ctx, `
		MERGE (r:Realm {novel_id: $nid, name: $realm})
		WITH r
		MATCH (c:Character {novel_id: $nid, name: $name})
		MERGE (c)-[b:BREAKTHROUGH {chapter: $ch}]->(r)
		  ON CREATE SET b.age = $age
	`, map[string]any{
		"nid":   novelID,
		"realm": realm,
		"name":  charName,
		"ch":    chapterNum,
		"age":   age,
	})
	if err != nil {
		return fmt.Errorf("breakthrough: %w", err)
	}
	return nil
}

// ── syncRelation ─────────────────────────────────────────────────────────────
// BELONGS_TO is now a real relationship edge (Character → Character).

func (w *GraphWriter) syncRelation(ctx context.Context, s neo4j.Session,
	novelID int64, chapterNum int, rel model.CharacterRelation) error {

	relType := normalizeRelType(rel.RelationType)
	if relType == "" {
		return fmt.Errorf("unknown relation type: %s", rel.RelationType)
	}

	// BELONGS_TO: ensure target (faction) exists as a Character node first
	if rel.RelationType == "BELONGS_TO" {
		if _, err := s.Run(ctx, `
			MERGE (f:Character {novel_id: $nid, name: $faction})
			  ON CREATE SET f.type = '势力', f.first = $ch, f.last = $ch
			  ON MATCH  SET f.last = $ch
		`, map[string]any{
			"nid":     novelID,
			"faction": rel.ToName,
			"ch":      chapterNum,
		}); err != nil {
			return fmt.Errorf("ensure faction: %w", err)
		}
	}

	ended := false
	desc := strings.ToLower(rel.Description)
	if strings.Contains(desc, "断裂") || strings.Contains(desc, "结束") || strings.Contains(desc, "决裂") {
		ended = true
	}

	_, err := s.Run(ctx, fmt.Sprintf(`
		MATCH (a:Character {novel_id: $nid, name: $from})
		MATCH (b:Character {novel_id: $nid, name: $to})
		MERGE (a)-[r:%s]->(b)
		  ON CREATE SET r.since = $ch, r.trigger = $trigger, r.description = $desc
		  ON MATCH  SET r.until = CASE WHEN $ended THEN $ch ELSE r.until END
	`, relType), map[string]any{
		"nid":     novelID,
		"from":    rel.FromName,
		"to":      rel.ToName,
		"ch":      chapterNum,
		"trigger": rel.TriggerEvent,
		"desc":    rel.Description,
		"ended":   ended,
	})
	if err != nil {
		return fmt.Errorf("merge relation %s: %w", relType, err)
	}
	return nil
}

// ── syncTechnique ─────────────────────────────────────────────────────────────
// level is now a property on the LEARNS edge (no TechniqueLevel node).

func (w *GraphWriter) syncTechnique(ctx context.Context, s neo4j.Session,
	novelID int64, chapterNum int, tech model.TechniqueInfo) error {

	_, err := s.Run(ctx, `
		MERGE (t:Technique {novel_id: $nid, name: $name})
		  ON CREATE SET t.description = $desc
		WITH t
		MATCH (c:Character {novel_id: $nid, name: $practitioner})
		MERGE (c)-[l:LEARNS {chapter: $ch}]->(t)
		  ON CREATE SET l.action = $action, l.level = $level, l.description = $desc
		  ON MATCH  SET l.action = CASE WHEN $action <> '' AND (l.action IS NULL OR $action = '突破') THEN $action ELSE l.action END,
		               l.level  = CASE WHEN $level  <> '' THEN $level  ELSE l.level END
	`, map[string]any{
		"nid":          novelID,
		"name":         tech.Name,
		"desc":         tech.Description,
		"practitioner": tech.Practitioner,
		"ch":           chapterNum,
		"action":       tech.Action,
		"level":        tech.Level,
	})
	if err != nil {
		return fmt.Errorf("merge technique: %w", err)
	}
	return nil
}

// ── syncEvent ────────────────────────────────────────────────────────────────

func (w *GraphWriter) syncEvent(ctx context.Context, s neo4j.Session,
	novelID int64, chapterNum int, chapterID int64, evt model.EventInfo) error {

	_, err := s.Run(ctx, `
		MERGE (e:Event {novel_id: $nid, title: $title})
		  ON CREATE SET e.summary = $summary, e.chapter = $ch
		WITH e
		MATCH (ch:Chapter {id: $chId})
		MERGE (e)-[:OCCURS_IN]->(ch)
	`, map[string]any{
		"nid":     novelID,
		"title":   evt.Title,
		"summary": evt.Summary,
		"ch":      chapterNum,
		"chId":    chapterID,
	})
	if err != nil {
		return fmt.Errorf("merge event: %w", err)
	}

	// Link participants
	for _, name := range evt.Participants {
		if name == "" || isNoisyName(name) {
			continue
		}
		if _, err := s.Run(ctx, `
			MATCH (c:Character {novel_id: $nid, name: $name})
			MATCH (e:Event    {novel_id: $nid, title: $title})
			MERGE (c)-[:INVOLVED_IN {chapter: $ch, role: $role}]->(e)
		`, map[string]any{
			"nid":   novelID,
			"name":  name,
			"title": evt.Title,
			"ch":    chapterNum,
			"role":  "参与者",
		}); err != nil {
			log.Printf("[graph] ⚠️ ch%d event[%s] participant[%s]: %v",
				chapterNum, evt.Title, name, err)
		}
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

var agePattern = regexp.MustCompile(`(\d+)\s*岁`)

func extractAge(status string) int {
	m := agePattern.FindStringSubmatch(status)
	if len(m) >= 2 {
		var age int
		fmt.Sscanf(m[1], "%d", &age)
		return age
	}
	return 0
}

// normalizeRelType maps AI-extracted relation type to Neo4j edge label.
// LOVE_INTEREST is renamed to shorter LOVES.
func normalizeRelType(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "MASTER_OF":
		return "MASTER_OF"
	case "FRIEND_OF":
		return "FRIEND_OF"
	case "ENEMY_OF":
		return "ENEMY_OF"
	case "LOVE_INTEREST":
		return "LOVES" // renamed
	case "BELONGS_TO":
		return "BELONGS_TO"
	default:
		return ""
	}
}

func isNoisyName(name string) bool {
	noise := map[string]bool{
		"师兄": true, "师弟": true, "师姐": true, "师妹": true,
		"师叔": true, "师伯": true, "师父": true, "师尊": true,
		"前辈": true, "道友": true, "主人": true, "小姐": true,
		"少爷": true, "夫人": true, "老爷": true, "老头": true,
		"老者": true, "大汉": true, "妇人": true, "少妇": true,
		"那人": true, "此人": true, "来人": true,
	}
	if noise[name] {
		return true
	}
	for _, suffix := range []string{"修士", "男子", "女子", "少年", "少女", "弟子", "门人", "书生", "儒生", "道人", "散修", "魔修"} {
		if strings.HasSuffix(name, suffix) && len([]rune(strings.TrimSuffix(name, suffix))) <= 2 {
			return true
		}
	}
	return false
}
