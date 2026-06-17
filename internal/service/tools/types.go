package tools

// --- Tool input types for Eino ADK tools ---

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

// GetChaptersInput is the input for the get_chapters tool.
type GetChaptersInput struct {
	Start int `json:"start" jsonschema_description:"起始章节号，默认 maxChapter - n + 1"`
	End   int `json:"end" jsonschema_description:"结束章节号，默认 maxChapter"`
	N     int `json:"n" jsonschema_description:"最近 N 章快捷写法，默认 5，最大 20。start 和 end 均为 0 时生效"`
}

// ChapterSummary is a single chapter summary returned to the LLM.
type ChapterSummary struct {
	ChapterNum int      `json:"chapter_num"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Characters []string `json:"characters"`
	Events     []string `json:"events"`
}

// QueryTechniquesInput is the input for the query_techniques tool.
type QueryTechniquesInput struct {
	CharacterName string `json:"character_name" jsonschema_description:"要查询的人物规范名称"`
}

// TechniqueEntry is a single technique acquisition record returned to the LLM.
type TechniqueEntry struct {
	Technique    string `json:"technique"`
	Level        string `json:"level,omitempty"`
	Action       string `json:"action"`
	Chapter      int    `json:"chapter"`
	Practitioner string `json:"practitioner,omitempty"`
	Description  string `json:"description,omitempty"`
}

// QueryAllTechniquesInput is the input for the query_all_techniques tool.
type QueryAllTechniquesInput struct {
	// No character filter — returns all techniques known up to the reading progress.
}

// QueryEventsInput is the input for the query_events tool.
type QueryEventsInput struct {
	CharacterName string `json:"character_name" jsonschema_description:"要查询的人物规范名称（必填），返回该人物参与的最近20个事件"`
}

// EventEntry is a single event record returned to the LLM.
type EventEntry struct {
	Title   string `json:"title"`
	Chapter int    `json:"chapter"`
	Summary string `json:"summary"`
	Role    string `json:"role,omitempty"`
}
