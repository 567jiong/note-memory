package agent

import "note-memory/internal/model"

// QueryClass represents the type of user question for routing within the agent graph.
type QueryClass string

const (
	QueryFact     QueryClass = "fact"     // "掌天瓶是什么" → PG semantic search
	QueryTimeline QueryClass = "timeline" // "境界突破年龄" → Neo4j
	QueryRelation QueryClass = "relation" // "韩立的仇敌" → Neo4j
	QueryMixed    QueryClass = "mixed"    // Both → merge
)

// ReadingAgentState is the state object that flows through all nodes in the Eino Graph.
// Each node receives and returns a pointer to this struct.
type ReadingAgentState struct {
	// === Immutable (request parameters) ===
	NovelID    int64  // current novel
	MaxChapter int    // spoiler-free boundary (1..MaxChapter)
	NovelTitle string // novel title for prompt injection
	Question   string // original user question

	// === Retrieval results ===
	SearchResults  []model.HybridSearchResult // results from hybrid search
	SearchQuery    string                     // current search query (may be rewritten)
	GraphTimeline  string                     // Neo4j realm timeline (formatted)
	GraphRelations string                     // Neo4j character relations (formatted)
	GraphStatus    string                     // Neo4j status timeline (formatted)

	// === Agent control flow ===
	Iteration      int    // current iteration count
	MaxIterations  int    // max iterations (default 3)
	RetrievalOK    bool   // LLM verdict: retrieval is sufficient
	MissingInfo    string // what information is missing (for query rewriting)
	RewrittenQuery string // LLM-rewritten query for next search iteration

	// === Final output ===
	FinalContext string // assembled context for LLM answer generation
	FinalAnswer  string // final answer returned to user
	QueryClass   string // routed query class (fact/timeline/relation/mixed)

	// === Diagnostics ===
	Error string // last error, if any
}
