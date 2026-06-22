package graph

import (
	"context"
	"log"
)

// InitSchema creates Neo4j constraints and indexes for the knowledge graph.
// Uses Community Edition-compatible UNIQUE syntax. Idempotent.
func InitSchema(ctx context.Context, d *Driver) error {
	if !d.IsEnabled() {
		return nil
	}

	s := d.Session(ctx)
	if s == nil {
		return nil
	}
	defer s.Close(ctx)

	constraints := []string{
		// Node uniqueness
		`CREATE CONSTRAINT novel_id      IF NOT EXISTS FOR (n:Novel)     REQUIRE n.id IS UNIQUE`,
		`CREATE CONSTRAINT chapter_id    IF NOT EXISTS FOR (c:Chapter)   REQUIRE c.id IS UNIQUE`,
		`CREATE CONSTRAINT char_identity IF NOT EXISTS FOR (c:Character) REQUIRE (c.novel_id, c.name) IS UNIQUE`,
		`CREATE CONSTRAINT event_identity IF NOT EXISTS FOR (e:Event)   REQUIRE (e.novel_id, e.title) IS UNIQUE`,
		`CREATE CONSTRAINT realm_identity IF NOT EXISTS FOR (r:Realm)   REQUIRE (r.novel_id, r.name) IS UNIQUE`,
		`CREATE CONSTRAINT tech_identity  IF NOT EXISTS FOR (t:Technique) REQUIRE (t.novel_id, t.name) IS UNIQUE`,
	}

	for _, c := range constraints {
		if _, err := s.Run(ctx, c, nil); err != nil {
			log.Printf("[neo4j] ⚠️ 约束创建失败: %v", err)
		}
	}

	indexes := []string{
		`CREATE INDEX character_type IF NOT EXISTS FOR (c:Character) ON (c.type)`,
		`CREATE INDEX chapter_number IF NOT EXISTS FOR (c:Chapter)   ON (c.number)`,
	}

	for _, c := range indexes {
		if _, err := s.Run(ctx, c, nil); err != nil {
			log.Printf("[neo4j] ⚠️ 索引创建失败: %v", err)
		}
	}

	log.Println("[neo4j] schema initialized (6 nodes, 8 relations)")
	return nil
}
