package qa

// --- Tool input types for Eino ADK tools ---

// searchChaptersInput is the input for the search_chapters tool.
type searchChaptersInput struct {
	Query string `json:"query" jsonschema_description:"搜索关键词（中文，空格分隔）"`
}

// chapterResult is a single chapter search result returned to the LLM.
type chapterResult struct {
	ChapterNum int     `json:"chapter_num"`
	Score      float64 `json:"score"`
	Summary    string  `json:"summary"`
	Content    string  `json:"content,omitempty"`
}

// queryTimelineInput is the input for the query_timeline tool.
type queryTimelineInput struct {
	CharacterName string `json:"character_name" jsonschema_description:"要查询的人物规范名称（如 韩立）"`
}

// timelineEntry is a single realm breakthrough entry.
type timelineEntry struct {
	Realm   string `json:"realm"`
	Chapter int    `json:"chapter"`
	Age     int    `json:"age,omitempty"`
}

// queryRelationsInput is the input for the query_relations tool.
type queryRelationsInput struct {
	CharacterName string `json:"character_name" jsonschema_description:"要查询的人物规范名称"`
}

// relationEntry is a single character relationship.
type relationEntry struct {
	From  string `json:"from"`
	To    string `json:"to"`
	RelType string `json:"rel_type"`
	Since int    `json:"since_chapter"`
	Ended int    `json:"ended_chapter,omitempty"`
}

// resolveEntityInput is the input for the resolve_entity tool.
type resolveEntityInput struct {
	Description string `json:"description" jsonschema_description:"用户描述的人物特征、别名、称号或身份"`
}
