package agent

// --- Tool input/output types for Eino ADK tools ---

// SearchChaptersInput is the input for the search_chapters tool.
type SearchChaptersInput struct {
	Query string `json:"query" jsonschema_description:"搜索关键词（中文，空格分隔）"`
}

// ChapterResult is a single chapter search result returned to the LLM.
type ChapterResult struct {
	ChapterNum int     `json:"chapter_num"`
	Score      float64 `json:"score"`
	Summary    string  `json:"summary"`
	Content    string  `json:"content,omitempty"`
}

// QueryTimelineInput is the input for the query_timeline tool.
type QueryTimelineInput struct {
	CharacterName string `json:"character_name" jsonschema_description:"要查询的人物规范名称（如 韩立）"`
}

// TimelineEntry is a single realm breakthrough entry.
type TimelineEntry struct {
	Realm   string `json:"realm"`
	Level   int    `json:"level"`
	Chapter int    `json:"chapter"`
	Age     int    `json:"age,omitempty"`
}

// QueryRelationsInput is the input for the query_relations tool.
type QueryRelationsInput struct {
	CharacterName string `json:"character_name" jsonschema_description:"要查询的人物规范名称"`
}

// RelationEntry is a single character relationship.
type RelationEntry struct {
	From     string `json:"from"`
	To       string `json:"to"`
	RelType  string `json:"rel_type"`
	Since    int    `json:"since_chapter"`
	Ended    int    `json:"ended_chapter,omitempty"`
}

// ResolveEntityInput is the input for the resolve_entity tool.
type ResolveEntityInput struct {
	Description string `json:"description" jsonschema_description:"用户描述的人物特征、别名、称号或身份"`
}
