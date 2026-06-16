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

	// 1. Ensure novel + chapter nodes
	_, err := s.Run(ctx, `
		MERGE (n:Novel {id: $novelId})
		  ON CREATE SET n.title = $novelTitle, n.author = $author
		MERGE (c:Chapter {id: $chapterId})
		  ON CREATE SET c.novel_id = $novelId, c.chapter_number = $num, c.title = $title
		MERGE (n)-[:HAS_CHAPTER]->(c)
	`, map[string]any{
		"novelId":    novel.ID,
		"novelTitle": novel.Title,
		"author":     novel.Author,
		"chapterId":  ch.ID,
		"num":        ch.ChapterNumber,
		"title":      ch.Title,
	})
	if err != nil {
		return fmt.Errorf("sync novel+chapter: %w", err)
	}

	// 2. UPSERT characters
	for _, char := range chars {
		if char.Name == "" || isNoisyName(char.Name) {
			continue
		}
		w.syncCharacter(ctx, s, novel.ID, ch.ChapterNumber, ch.ID, char)
	}

	// 3. UPSERT events
	for _, evt := range events {
		if evt.Title == "" {
			continue
		}
		w.syncEvent(ctx, s, novel.ID, ch.ChapterNumber, ch.ID, evt)
	}

	// 4. UPSERT character relationships
	for _, rel := range relations {
		if rel.FromName == "" || rel.ToName == "" || rel.RelationType == "" {
			continue
		}
		if isNoisyName(rel.FromName) || isNoisyName(rel.ToName) {
			continue
		}
		w.syncRelation(ctx, s, novel.ID, ch.ChapterNumber, rel)
	}

	// 5. UPSERT techniques
	for _, tech := range techniques {
		if tech.Name == "" || tech.Practitioner == "" {
			continue
		}
		w.syncTechnique(ctx, s, novel.ID, ch.ChapterNumber, tech)
	}

	log.Printf("[graph] chapter %d synced: %d characters, %d events, %d relations, %d techniques",
		ch.ChapterNumber, len(chars), len(events), len(relations), len(techniques))
	return nil
}

func (w *GraphWriter) syncCharacter(ctx context.Context, s neo4j.Session, novelID int64, chapterNum int, chapterID int64, char model.CharacterInfo) {
	// Use AI-classified type; fallback to "配角" if empty (conservative — avoids false "主角")
	charType := char.Type
	if charType == "" {
		charType = "配角"
	}

	s.Run(ctx, `
		MERGE (c:Character {novel_id: $novelId, name: $name})
		  ON CREATE SET
		    c.type = $type,
		    c.first_appearance_chapter = $chapterNum,
		    c.last_appearance_chapter = $chapterNum
		  ON MATCH SET
		    c.type = CASE WHEN c.type = '路人' AND $type != '路人' THEN $type ELSE c.type END,
		    c.last_appearance_chapter = $chapterNum
		WITH c
		MATCH (ch:Chapter {id: $chapterId})
		MERGE (c)-[r:APPEARS_IN]->(ch)
		  ON CREATE SET r.status = $status, r.age = $age
		  ON MATCH SET r.status = $status
	`, map[string]any{
		"novelId":   novelID,
		"name":      char.Name,
		"type":      charType,
		"chapterNum": chapterNum,
		"chapterId": chapterID,
		"status":    char.Status,
		"age":       extractAge(char.Status),
	})

	// Sync realm breakthrough (uses LLM-extracted realm from CharacterInfo)
	if char.Realm != "" {
		w.syncRealmBreakthrough(ctx, s, novelID, char.Name, chapterNum, char.Realm, extractAge(char.Status))
	}
}

func (w *GraphWriter) syncRealmBreakthrough(ctx context.Context, s neo4j.Session, novelID int64, charName string, chapterNum int, realm string, age int) {
	s.Run(ctx, `
		MERGE (r:Realm {novel_id: $novelId, name: $realm})
		WITH r
		MATCH (c:Character {novel_id: $novelId, name: $name})
		MERGE (c)-[b:BREAKTHROUGH_TO {at_chapter: $chapterNum}]->(r)
		  ON CREATE SET b.age = $age
	`, map[string]any{
		"novelId":   novelID,
		"realm":     realm,
		"name":      charName,
		"chapterNum": chapterNum,
		"age":       age,
	})
}

func (w *GraphWriter) syncRelation(ctx context.Context, s neo4j.Session, novelID int64, chapterNum int, rel model.CharacterRelation) {
	// BELONGS_TO: "to" is an organization name, not a character — handle as a string property
	if rel.RelationType == "BELONGS_TO" {
		s.Run(ctx, `
			MATCH (a:Character {novel_id: $novelId, name: $fromName})
			SET a.faction = $toName
		`, map[string]any{
			"novelId":  novelID,
			"fromName": rel.FromName,
			"toName":   rel.ToName,
		})
		return
	}

	relType := normalizeRelType(rel.RelationType)
	if relType == "" {
		return
	}

	// Determine if relationship is ending
	ended := false
	desc := strings.ToLower(rel.Description)
	if strings.Contains(desc, "断裂") || strings.Contains(desc, "结束") || strings.Contains(desc, "决裂") {
		ended = true
	}

	cypher := fmt.Sprintf(`
		MATCH (a:Character {novel_id: $novelId, name: $fromName})
		MATCH (b:Character {novel_id: $novelId, name: $toName})
		MERGE (a)-[r:%s]->(b)
		  ON CREATE SET
		    r.since_chapter = $chapterNum,
		    r.trigger_event = $triggerEvent,
		    r.description = $description
		  ON MATCH SET
		    r.ended_chapter = CASE WHEN $ended THEN $chapterNum ELSE r.ended_chapter END
	`, relType)

	s.Run(ctx, cypher, map[string]any{
		"novelId":      novelID,
		"fromName":     rel.FromName,
		"toName":       rel.ToName,
		"chapterNum":   chapterNum,
		"triggerEvent": rel.TriggerEvent,
		"description":  rel.Description,
		"ended":        ended,
	})

	// Link relationship to the triggering event if specified
	if rel.TriggerEvent != "" {
		s.Run(ctx, `
			MATCH (a:Character {novel_id: $novelId, name: $fromName})
			      -[r]->(b:Character {novel_id: $novelId, name: $toName})
			MATCH (e:Event {novel_id: $novelId, title: $eventTitle})
			MERGE (r)-[:TRIGGERED_BY]->(e)
		`, map[string]any{
			"novelId":    novelID,
			"fromName":   rel.FromName,
			"toName":     rel.ToName,
			"eventTitle": rel.TriggerEvent,
		})
	}
}

func (w *GraphWriter) syncTechnique(ctx context.Context, s neo4j.Session, novelID int64, chapterNum int, tech model.TechniqueInfo) {
	// Create Technique node
	s.Run(ctx, `
		MERGE (t:Technique {novel_id: $novelId, name: $name})
		  ON CREATE SET t.description = $description
	`, map[string]any{
		"novelId":     novelID,
		"name":        tech.Name,
		"description": tech.Description,
	})

	// Create TechniqueLevel if a level is specified
	if tech.Level != "" {
		s.Run(ctx, `
			MERGE (t:Technique {novel_id: $novelId, name: $techName})
			MERGE (tl:TechniqueLevel {novel_id: $novelId, technique_name: $techName, level_name: $level})
			  ON CREATE SET tl.description = $description
			MERGE (t)-[:HAS_LEVEL]->(tl)
		`, map[string]any{
			"novelId":     novelID,
			"techName":    tech.Name,
			"level":       tech.Level,
			"description": tech.Description,
		})

		// Character reaches this level
		s.Run(ctx, `
			MATCH (c:Character {novel_id: $novelId, name: $practitioner})
			MATCH (tl:TechniqueLevel {novel_id: $novelId, technique_name: $techName, level_name: $level})
			MERGE (c)-[lr:LEARNS_LEVEL {at_chapter: $chapterNum}]->(tl)
			  ON CREATE SET lr.action = $action
		`, map[string]any{
			"novelId":      novelID,
			"practitioner": tech.Practitioner,
			"techName":     tech.Name,
			"level":        tech.Level,
			"chapterNum":   chapterNum,
			"action":       tech.Action,
		})
	}

	// Character learns technique
	s.Run(ctx, `
		MATCH (c:Character {novel_id: $novelId, name: $practitioner})
		MATCH (t:Technique {novel_id: $novelId, name: $techName})
		MERGE (c)-[l:LEARNS]->(t)
		  ON CREATE SET l.at_chapter = $chapterNum, l.action = $action, l.description = $description
		  ON MATCH SET l.action = CASE WHEN $action = '突破' OR l.action IS NULL THEN $action ELSE l.action END
	`, map[string]any{
		"novelId":      novelID,
		"practitioner": tech.Practitioner,
		"techName":     tech.Name,
		"chapterNum":   chapterNum,
		"action":       tech.Action,
		"description":  tech.Description,
	})
}

func (w *GraphWriter) syncEvent(ctx context.Context, s neo4j.Session, novelID int64, chapterNum int, chapterID int64, evt model.EventInfo) {
	s.Run(ctx, `
		MERGE (e:Event {novel_id: $novelId, title: $title})
		  ON CREATE SET e.summary = $summary, e.impact = $impact, e.chapter_number = $chapterNum
		WITH e
		MATCH (ch:Chapter {id: $chapterId})
		MERGE (e)-[:HAPPENS_IN]->(ch)
	`, map[string]any{
		"novelId":    novelID,
		"title":      evt.Title,
		"summary":    evt.Summary,
		"impact":     evt.Impact,
		"chapterNum": chapterNum,
		"chapterId":  chapterID,
	})

	for _, name := range evt.Participants {
		if name == "" || isNoisyName(name) {
			continue
		}
		s.Run(ctx, `
			MATCH (c:Character {novel_id: $novelId, name: $name})
			MATCH (e:Event {novel_id: $novelId, title: $title})
			MERGE (c)-[:PARTICIPATES_IN {role: '参与者', at_chapter: $chapterNum}]->(e)
		`, map[string]any{
			"novelId":    novelID,
			"name":       name,
			"title":      evt.Title,
			"chapterNum": chapterNum,
		})
	}
}

// ---- Helpers ----

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

// normalizeRelType maps AI-extracted relation types to Neo4j edge labels.
func normalizeRelType(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "MASTER_OF":
		return "MASTER_OF"
	case "FRIEND_OF":
		return "FRIEND_OF"
	case "ENEMY_OF":
		return "ENEMY_OF"
	case "LOVE_INTEREST":
		return "LOVE_INTEREST"
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
