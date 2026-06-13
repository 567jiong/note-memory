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

// SyncChapter writes chapter entities and relationships to Neo4j.
func (w *GraphWriter) SyncChapter(ctx context.Context, novel *model.Novel, ch *model.Chapter, chars []model.CharacterInfo, events []model.EventInfo) error {
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

	log.Printf("[graph] chapter %d synced: %d characters, %d events", ch.ChapterNumber, len(chars), len(events))
	return nil
}

func (w *GraphWriter) syncCharacter(ctx context.Context, s neo4j.Session, novelID int64, chapterNum int, chapterID int64, char model.CharacterInfo) {
	charType := detectCharType(char.Name, chapterNum, char.FirstAppearance)

	s.Run(ctx, `
		MERGE (c:Character {novel_id: $novelId, name: $name})
		  ON CREATE SET
		    c.type = $type,
		    c.first_appearance_chapter = $chapterNum,
		    c.last_appearance_chapter = $chapterNum
		  ON MATCH SET
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

	// Detect realm breakthroughs
	if realm := detectRealmChange(char.Status); realm != "" {
		w.syncRealmBreakthrough(ctx, s, novelID, char.Name, chapterNum, realm, extractAge(char.Status))
	}
}

func (w *GraphWriter) syncRealmBreakthrough(ctx context.Context, s neo4j.Session, novelID int64, charName string, chapterNum int, realm string, age int) {
	s.Run(ctx, `
		MERGE (r:Realm {novel_id: $novelId, name: $realm})
		  ON CREATE SET r.level = $level
		WITH r
		MATCH (c:Character {novel_id: $novelId, name: $name})
		MERGE (c)-[b:BREAKTHROUGH_TO {at_chapter: $chapterNum}]->(r)
		  ON CREATE SET b.age = $age
	`, map[string]any{
		"novelId":   novelID,
		"realm":     realm,
		"level":     realmLevel(realm),
		"name":      charName,
		"chapterNum": chapterNum,
		"age":       age,
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

// ---- Detection helpers ----

var realmPatterns = []struct {
	pattern *regexp.Regexp
	realm   string
}{
	{regexp.MustCompile(`练气`), "练气期"},
	{regexp.MustCompile(`筑基`), "筑基期"},
	{regexp.MustCompile(`金丹|结丹`), "金丹期"},
	{regexp.MustCompile(`元婴`), "元婴期"},
	{regexp.MustCompile(`化神`), "化神期"},
	{regexp.MustCompile(`炼虚`), "炼虚期"},
	{regexp.MustCompile(`合体`), "合体期"},
	{regexp.MustCompile(`大乘`), "大乘期"},
	{regexp.MustCompile(`渡劫`), "渡劫期"},
	{regexp.MustCompile(`真仙`), "真仙境"},
	{regexp.MustCompile(`金仙`), "金仙境"},
	{regexp.MustCompile(`太乙`), "太乙境"},
	{regexp.MustCompile(`大罗`), "大罗境"},
	{regexp.MustCompile(`道祖`), "道祖境"},
}

func detectRealmChange(status string) string {
	for _, rp := range realmPatterns {
		if rp.pattern.MatchString(status) {
			return rp.realm
		}
	}
	return ""
}

func realmLevel(realm string) int {
	switch realm {
	case "练气期":
		return 1
	case "筑基期":
		return 2
	case "金丹期":
		return 3
	case "元婴期":
		return 4
	case "化神期":
		return 5
	case "炼虚期":
		return 6
	case "合体期":
		return 7
	case "大乘期":
		return 8
	case "渡劫期":
		return 9
	case "真仙境":
		return 10
	case "金仙境":
		return 11
	case "太乙境":
		return 12
	case "大罗境":
		return 13
	case "道祖境":
		return 14
	default:
		return 0
	}
}

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

func detectCharType(name string, chapterNum int, firstAppearance int) string {
	if chapterNum <= 3 && firstAppearance <= 3 {
		return "主角"
	}
	if firstAppearance > 0 && firstAppearance <= 5 {
		return "重要配角"
	}
	return "配角"
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
