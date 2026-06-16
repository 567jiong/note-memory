package graph

import (
	"context"
	"log"
)

// InitSchema creates Neo4j constraints and indexes for the knowledge graph.
// Idempotent — safe to call on every startup.
func InitSchema(ctx context.Context, d *Driver) error {
	if !d.IsEnabled() {
		return nil
	}

	constraints := []string{
		// Novel / Chapter
		`CREATE CONSTRAINT novel_id IF NOT EXISTS FOR (n:Novel) REQUIRE n.id IS UNIQUE`,
		`CREATE CONSTRAINT chapter_id IF NOT EXISTS FOR (c:Chapter) REQUIRE c.id IS UNIQUE`,

		// Character — uniqueness on (novel_id, name) so MERGE works correctly
		`CREATE CONSTRAINT character_novel_name IF NOT EXISTS FOR (c:Character) REQUIRE (c.novel_id, c.name) IS NODE KEY`,

		// Technique — uniqueness on (novel_id, name)
		`CREATE CONSTRAINT technique_novel_name IF NOT EXISTS FOR (t:Technique) REQUIRE (t.novel_id, t.name) IS NODE KEY`,

		// Indexes for fast lookup
		`CREATE INDEX character_type IF NOT EXISTS FOR (c:Character) ON (c.type)`,
		`CREATE INDEX chapter_number IF NOT EXISTS FOR (c:Chapter) ON (c.chapter_number)`,
		`CREATE INDEX technique_name IF NOT EXISTS FOR (t:Technique) ON (t.name)`,
		`CREATE INDEX technique_level_name IF NOT EXISTS FOR (tl:TechniqueLevel) ON (tl.level_name)`,
	}

	s := d.Session(ctx)
	if s == nil {
		return nil
	}
	defer s.Close(ctx)

	for _, cypher := range constraints {
		_, err := s.Run(ctx, cypher, nil)
		if err != nil {
			log.Printf("[neo4j] constraint warning: %v", err)
		}
	}

	log.Println("[neo4j] schema initialized")
	return nil
}
