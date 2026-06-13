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
		`CREATE CONSTRAINT novel_id IF NOT EXISTS FOR (n:Novel) REQUIRE n.id IS UNIQUE`,
		`CREATE CONSTRAINT chapter_id IF NOT EXISTS FOR (c:Chapter) REQUIRE c.id IS UNIQUE`,
		`CREATE CONSTRAINT character_key IF NOT EXISTS FOR (c:Character) REQUIRE (c.novel_id, c.name) IS NODE KEY`,
		`CREATE CONSTRAINT item_key IF NOT EXISTS FOR (i:Item) REQUIRE (i.novel_id, i.name) IS NODE KEY`,
		`CREATE CONSTRAINT realm_key IF NOT EXISTS FOR (r:Realm) REQUIRE (r.novel_id, r.name) IS NODE KEY`,
		`CREATE CONSTRAINT event_key IF NOT EXISTS FOR (e:Event) REQUIRE (e.novel_id, e.title) IS NODE KEY`,
		`CREATE CONSTRAINT location_key IF NOT EXISTS FOR (l:Location) REQUIRE (l.novel_id, l.name) IS NODE KEY`,
		`CREATE CONSTRAINT faction_key IF NOT EXISTS FOR (f:Faction) REQUIRE (f.novel_id, f.name) IS NODE KEY`,

		`CREATE INDEX character_name IF NOT EXISTS FOR (c:Character) ON (c.name)`,
		`CREATE INDEX character_type IF NOT EXISTS FOR (c:Character) ON (c.type)`,
		`CREATE INDEX chapter_number IF NOT EXISTS FOR (c:Chapter) ON (c.chapter_number)`,
		`CREATE INDEX realm_level IF NOT EXISTS FOR (r:Realm) ON (r.level)`,
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
