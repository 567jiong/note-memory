package parser

import (
	"regexp"
	"strconv"
	"strings"
)

// ChapterInfo holds parsed chapter data.
type ChapterInfo struct {
	Number  int
	Title   string
	Content string
}

// maxChapterHeaderLen is the maximum length (in runes) a line can be to be
// considered a chapter header. Genuine chapter titles are typically short;
// content lines that happen to start with chapter-like patterns are longer.
const maxChapterHeaderLen = 60

// chapterPatterns is a list of regex patterns for matching chapter/section headers.
// Each pattern must have exactly one capture group for the chapter number (Arabic digits).
var chapterPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^第\s*([0-9]+)\s*章`),                               // 第1章
	regexp.MustCompile(`^第\s*([0-9]+)\s*节`),                               // 第1节
	regexp.MustCompile(`^(?i)Chapter\s+([0-9]+)`),                           // Chapter 1
	regexp.MustCompile(`^(?i)Ch\.\s*([0-9]+)`),                             // Ch. 1
	regexp.MustCompile(`^第[一二三四五六七八九十百千0-9]+卷\s*第\s*([0-9]+)\s*章`), // 第一卷 第一章
}

// chineseNumerals maps Chinese numeral characters to values.
var chineseNumerals = map[rune]int{
	'零': 0, '〇': 0,
	'一': 1, '壹': 1,
	'二': 2, '贰': 2, '两': 2,
	'三': 3, '叁': 3,
	'四': 4, '肆': 4,
	'五': 5, '伍': 5,
	'六': 6, '陆': 6,
	'七': 7, '柒': 7,
	'八': 8, '捌': 8,
	'九': 9, '玖': 9,
	'十': 10, '拾': 10,
	'百': 100, '佰': 100,
	'千': 1000, '仟': 1000,
}

// parseChineseNumber converts a Chinese numeral string (e.g., "一百二十三") to an integer.
func parseChineseNumber(s string) int {
	if s == "" {
		return 0
	}
	// Try Arabic numeral first
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}

	runes := []rune(s)
	total := 0
	section := 0
	for _, r := range runes {
		v, ok := chineseNumerals[r]
		if !ok {
			continue
		}
		if v >= 10 {
			if section == 0 {
				section = 1
			}
			section *= v
			total += section
			section = 0
		} else {
			section = v
		}
	}
	total += section
	return total
}

// chineseChapterPattern matches patterns like "第一章", "第一回" etc.
var chineseChapterPattern = regexp.MustCompile(`^第([零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+)\s*([章回节卷集篇])`)

// isChineseChapterHeader checks if a line is a Chinese-numeral chapter header.
func isChineseChapterHeader(line string) (bool, int) {
	matches := chineseChapterPattern.FindStringSubmatch(line)
	if len(matches) >= 3 {
		num := parseChineseNumber(matches[1])
		if num > 0 {
			return true, num
		}
	}
	return false, 0
}

// Parse splits raw text content of a novel into chapters.
// It scans line by line looking for chapter headers.
func Parse(content string) []ChapterInfo {
	lines := strings.Split(content, "\n")
	var chapters []ChapterInfo
	var currentChapter *ChapterInfo
	var currentContent strings.Builder

	flushChapter := func() {
		if currentChapter != nil {
			currentChapter.Content = strings.TrimSpace(currentContent.String())
			chapters = append(chapters, *currentChapter)
			currentContent.Reset()
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if currentChapter != nil {
				currentContent.WriteString(line)
				currentContent.WriteString("\n")
			}
			continue
		}

		chapterNum := 0
		matched := false

		// Only consider short lines as potential chapter headers.
		// Content lines containing chapter-like patterns are typically much longer.
		if len([]rune(trimmed)) <= maxChapterHeaderLen {
			// Try Chinese numeral chapter headers first
			if ok, num := isChineseChapterHeader(trimmed); ok {
				chapterNum = num
				matched = true
			}
			// Try Arabic numeral patterns
			if !matched {
				for _, pat := range chapterPatterns {
					matches := pat.FindStringSubmatch(trimmed)
					if len(matches) >= 2 {
						if n, err := strconv.Atoi(matches[1]); err == nil {
							chapterNum = n
							matched = true
							break
						}
					}
				}
			}
		}

		if matched && chapterNum > 0 {
			flushChapter()
			currentChapter = &ChapterInfo{
				Number: chapterNum,
				Title:  trimmed,
			}
		} else if currentChapter != nil {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		} else {
			// Before the first chapter — start preamble
			if currentChapter == nil {
				currentChapter = &ChapterInfo{
					Number: 0,
					Title:  "前言/简介",
				}
			}
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}

	flushChapter()

	// Renumber chapters sequentially
	chapters = renumberChapters(chapters)

	// Drop preamble if it's empty and there are real chapters
	if len(chapters) > 1 && chapters[0].Number == 0 && strings.TrimSpace(chapters[0].Content) == "" {
		chapters = chapters[1:]
	}

	return chapters
}

// renumberChapters fixes chapter numbering to be sequential starting from 1.
func renumberChapters(chapters []ChapterInfo) []ChapterInfo {
	if len(chapters) == 0 {
		return chapters
	}

	hasPreamble := len(chapters) > 0 && chapters[0].Number == 0
	startIdx := 0
	if hasPreamble {
		startIdx = 1
	}

	for i := startIdx; i < len(chapters); i++ {
		chapters[i].Number = i - startIdx + 1
	}

	return chapters
}

// DetectNovelTitle tries to find the novel title from content.
func DetectNovelTitle(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if match := regexp.MustCompile(`《([^》]+)》`).FindStringSubmatch(trimmed); len(match) >= 2 {
			return match[1]
		}
	}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len([]rune(trimmed)) > 50 {
				return string([]rune(trimmed)[:50]) + "..."
			}
			return trimmed
		}
	}
	return "未命名小说"
}
