package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"note-memory/internal/ai"
	"note-memory/internal/parser"
	"strings"
)

// llmMetaResult is the structured output expected from the LLM.
type llmMetaResult struct {
	Title   string `json:"title"`
	Author  string `json:"author"`
	Summary string `json:"summary"`
}

// extractMetaText takes the content before the first chapter as the meta region.
// Falls back to first 4000 chars if no chapter boundary is found.
func extractMetaText(content string) string {
	parsed := parser.Parse(content)
	if len(parsed) == 0 {
		return truncateRunes(content, 4000)
	}

	firstChapter := parsed[0]
	if firstChapter.Content == "" {
		return truncateRunes(content, 4000)
	}

	// Find where the first chapter starts in the original content
	idx := strings.Index(content, firstChapter.Content)
	if idx < 0 {
		return truncateRunes(content, 4000)
	}

	meta := content[:idx]
	return truncateRunes(meta, 5000)
}

// llmExtractMeta uses LLM to extract novel metadata with retry-and-validate loop.
// Falls back to regex parser on failure.
func llmExtractMeta(ctx context.Context, aiClient *ai.Client, content string) (title, author string) {
	metaText := extractMetaText(content)
	if strings.TrimSpace(metaText) == "" {
		return parser.DetectNovelMeta(content).Title, parser.DetectNovelMeta(content).Author
	}

	sysPrompt := `你是一个小说元数据提取器。你的任务是从小说文件的开头部分提取书名(title)和作者(author)。

## 规则（非常重要）
- title 是小说书名，不是章节名、不是卷名、不是网站名
- 如果原文中出现"第一章""第X卷""第X回"等，那不是书名，不要提取
- author 是作者笔名，如果找不到就填空字符串
- 书名通常出现在第一行或第二行，格式可能是：
  · 《书名》
  · 书名 作者：作者名
  · 书名 / 作者：作者名
  · 书名（其他说明文字）
- 网站广告、声明文字、更新日志等不是元数据，忽略
- 如果内容直接以正文开头（没有元数据区），title 和 author 都留空

## 输出格式
严格输出 JSON，不要任何额外文字：
{"title":"书名","author":"作者","summary":""}`

	var lastErrors []string

	for attempt := 0; attempt < 3; attempt++ {
		userPrompt := metaText
		if len(lastErrors) > 0 {
			userPrompt += fmt.Sprintf("\n\n## 上次提取错误，请修正：%s", strings.Join(lastErrors, "; "))
		}

		resp, err := aiClient.Chat(ctx, []ai.Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userPrompt},
		}, 0.2, 300)
		if err != nil {
			log.Printf("[meta] LLM call attempt %d failed: %v", attempt, err)
			continue
		}

		var result llmMetaResult
		if err := json.Unmarshal([]byte(cleanJSON(resp)), &result); err != nil {
			log.Printf("[meta] JSON parse attempt %d failed: %v (raw: %s)", attempt, err, resp)
			lastErrors = []string{"JSON格式错误，请严格输出合法JSON"}
			continue
		}

		errs := validateLLMMeta(result)
		if len(errs) == 0 {
			log.Printf("[meta] LLM extraction success: title=%q author=%q", result.Title, result.Author)
			return result.Title, result.Author
		}

		lastErrors = errs
		log.Printf("[meta] validation attempt %d failed: %v", attempt, errs)
	}

	// Fallback to regex
	log.Printf("[meta] LLM extraction failed after 3 attempts, falling back to regex")
	fallback := parser.DetectNovelMeta(content)
	return fallback.Title, fallback.Author
}

// validateLLMMeta checks the LLM result for obvious errors.
func validateLLMMeta(r llmMetaResult) []string {
	var errs []string
	title := strings.TrimSpace(r.Title)

	if title == "" {
		errs = append(errs, "title为空")
	} else {
		if len([]rune(title)) > 40 {
			errs = append(errs, fmt.Sprintf("title过长(%d字)，可能不是书名", len([]rune(title))))
		}
		chapterPatterns := []string{"章", "卷", "回", "节", "篇"}
		for _, p := range chapterPatterns {
			if strings.Contains(title, p) {
				errs = append(errs, fmt.Sprintf("title包含'%s'，可能是章节名而非书名", p))
				break
			}
		}
		adPatterns := []string{"精校", "全本", "完结", "最新", "更新", "http", "www"}
		for _, p := range adPatterns {
			if strings.Contains(strings.ToLower(title), strings.ToLower(p)) {
				errs = append(errs, fmt.Sprintf("title包含广告词'%s'", p))
				break
			}
		}
	}
	// author can be empty — many TXTs don't label the author
	return errs
}

// cleanJSON extracts JSON from LLM response (handles markdown code blocks).
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` wrapper
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	// Find first { and last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
