package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// DefaultTestCases returns the built-in evaluation test suite.
// These are designed for Chinese web-novel reading memory scenarios.
func DefaultTestCases() []*TestCase {
	return []*TestCase{
		// ── 境界查询（测 query_timeline）──────────────────────────
		{
			ID:          "realm-001",
			Description: "境界查询：当前主角境界",
			Question:    "主角现在是什么境界？",
			ExpectedTools:  []string{"query_timeline"},
			MustNotMention: []string{"第999章"}, // 不应引用进度之后的内容
			MaxIterations:  3,
		},
		{
			ID:          "realm-002",
			Description: "境界查询：具体突破时间",
			Question:    "主角是什么时候突破到金丹期的？",
			ExpectedTools:  []string{"query_timeline"},
			MaxIterations:  3,
		},

		// ── 人物关系（测 query_relations + resolve_entity）────────
		{
			ID:          "rel-001",
			Description: "关系查询：师徒关系",
			Question:    "主角的师父是谁？",
			ExpectedTools:  []string{"query_relations"},
			MustMention:    []string{}, // 具体名字取决于小说
			MaxIterations:  3,
		},
		{
			ID:          "rel-002",
			Description: "关系查询：使用别名需要先 resolve",
			Question:    "韩跑跑有哪些仇敌？",
			ExpectedTools:  []string{"resolve_entity", "query_relations"},
			MaxIterations:  4,
		},

		// ── 功法查询（测 query_techniques / query_all_techniques）─
		{
			ID:          "tech-001",
			Description: "功法查询：主角修炼的功法",
			Question:    "主角修炼了哪些功法？",
			ExpectedTools:  []string{"query_techniques"},
			MaxIterations:  3,
		},
		{
			ID:          "tech-002",
			Description: "功法查询：全局功法一览",
			Question:    "这本书里有哪些厉害的功法？",
			ExpectedTools:  []string{"query_all_techniques"},
			MaxIterations:  3,
		},

		// ── 剧情回顾（测 search_chapters + get_chapters）─────────
		{
			ID:          "plot-001",
			Description: "剧情回顾：最近发生了什么",
			Question:    "最近几章讲了什么？",
			ExpectedTools:  []string{"get_chapters"},
			MaxIterations:  3,
		},
		{
			ID:          "plot-002",
			Description: "剧情搜索：具体事件",
			Question:    "主角在拍卖会上做了什么？",
			ExpectedTools:  []string{"search_chapters"},
			MaxIterations:  4,
		},

		// ── 事件查询（测 query_events）───────────────────────────
		{
			ID:          "event-001",
			Description: "事件查询：主角经历的大事",
			Question:    "主角经历了哪些重大事件？",
			ExpectedTools:  []string{"query_events"},
			MaxIterations:  3,
		},

		// ── 边界/对抗用例 ─────────────────────────────────────────
		{
			ID:          "edge-001",
			Description: "边界：不存在的角色",
			Question:    "张三丰是什么境界？",
			// 不应调用 query_timeline（resolve 之后会发现不存在）
			ForbiddenTools: []string{"query_timeline"},
			MaxIterations:  3,
		},
		{
			ID:          "edge-002",
			Description: "边界：空信息问题",
			Question:    "第99999章讲了什么？",
			// 不应无谓地调用多个工具
			MaxIterations: 2,
		},
	}
}

// LoadTestCases reads test cases from a JSON file.
func LoadTestCases(path string) ([]*TestCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read test cases: %w", err)
	}
	var cases []*TestCase
	if err := json.Unmarshal(b, &cases); err != nil {
		return nil, fmt.Errorf("unmarshal test cases: %w", err)
	}
	return cases, nil
}

// SaveTestCases writes test cases to a JSON file.
func SaveTestCases(cases []*TestCase, path string) error {
	b, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal test cases: %w", err)
	}
	return os.WriteFile(path, b, 0644)
}
