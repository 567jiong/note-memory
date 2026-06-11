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

// NovelMeta holds detected novel metadata.
type NovelMeta struct {
	Title  string
	Author string
}

const maxChapterHeaderLen = 60

// chapterPatterns for Arabic-numeral chapter headers.
var chapterPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^第\s*([0-9]+)\s*章`),
	regexp.MustCompile(`^第\s*([0-9]+)\s*节`),
	regexp.MustCompile(`^(?i)Chapter\s+([0-9]+)`),
	regexp.MustCompile(`^(?i)Ch\.\s*([0-9]+)`),
}

var chineseNumerals = map[rune]int{
	'零': 0, '〇': 0, '一': 1, '壹': 1, '二': 2, '贰': 2, '两': 2,
	'三': 3, '叁': 3, '四': 4, '肆': 4, '五': 5, '伍': 5,
	'六': 6, '陆': 6, '七': 7, '柒': 7, '八': 8, '捌': 8,
	'九': 9, '玖': 9, '十': 10, '拾': 10, '百': 100, '佰': 100, '千': 1000, '仟': 1000,
}

func parseChineseNumber(s string) int {
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	runes := []rune(s)
	total, section := 0, 0
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
	return total + section
}

// chineseChapterPattern matches "第一章"/"第一回"/"第一节"/"第一篇".
// "卷" is excluded — standalone volume headers are not chapter boundaries.
var chineseChapterPattern = regexp.MustCompile(`^第([零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+)\s*([章回节篇])`)

// stripVolumePrefix removes "第X卷 " prefix so "第一卷 第一章 标题" → "第一章 标题".
var volPrefixRe = regexp.MustCompile(`^第[零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟0-9]+\s*卷\s+`)

func stripVolumePrefix(line string) string {
	return volPrefixRe.ReplaceAllString(line, "")
}

// isVolumeOnly checks if a line is a volume-only header (has "第X卷" but no chapter marker).
// "第一卷 七玄门风云" → true
// "第一卷 第一章 标题" → false (has 第一章)
// "第1章 穿越"        → false (no 卷 marker at all)
func isVolumeOnly(line string) bool {
	t := strings.TrimSpace(line)
	// Must contain a volume marker
	if !strings.Contains(t, "卷") {
		return false
	}
	if !volPrefixRe.MatchString(t) {
		return false
	}
	stripped := strings.TrimSpace(stripVolumePrefix(t))
	if stripped == "" {
		return true
	}
	// After stripping volume, if there's a chapter marker → not volume-only
	return !chineseChapterPattern.MatchString(stripped)
}

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

func isPreambleLine(line string) bool {
	t := strings.TrimSpace(line)
	for _, p := range []string{
		"===", "***", "---", "___",
		"更多精校", "更多小说", "请加", "QQ", "qq",
		"http://", "https://", "www.",
		"内容简介", "作品简介", "书籍简介", "简介：", "介绍：",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// Parse splits raw text content into chapters.
func Parse(content string) []ChapterInfo {
	lines := strings.Split(content, "\n")
	var chapters []ChapterInfo
	var currentChapter *ChapterInfo
	var currentContent strings.Builder
	inPreamble := true

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

		// Skip known preamble banner lines before first chapter
		if inPreamble && isPreambleLine(trimmed) {
			continue
		}

		// Volume-only headers are content, not chapter boundaries
		if isVolumeOnly(trimmed) {
			inPreamble = false
			if currentChapter == nil {
				currentChapter = &ChapterInfo{Number: 0, Title: "前言/简介"}
			}
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
			continue
		}

		// Strip volume prefix for chapter matching
		checkLine := strings.TrimSpace(stripVolumePrefix(trimmed))

		chapterNum := 0
		matched := false

		if len([]rune(checkLine)) <= maxChapterHeaderLen {
			// Chinese chapter
			if ok, num := isChineseChapterHeader(checkLine); ok {
				chapterNum = num
				matched = true
			}
			// Arabic chapter
			if !matched {
				for _, pat := range chapterPatterns {
					m := pat.FindStringSubmatch(checkLine)
					if len(m) >= 2 {
						if n, err := strconv.Atoi(m[1]); err == nil {
							chapterNum = n
							matched = true
							break
						}
					}
				}
			}
		}

		if matched && chapterNum > 0 {
			inPreamble = false
			flushChapter()
			currentChapter = &ChapterInfo{Number: chapterNum, Title: trimmed}
		} else if currentChapter != nil {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		} else {
			if currentChapter == nil {
				currentChapter = &ChapterInfo{Number: 0, Title: "前言/简介"}
			}
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}

	flushChapter()

	chapters = renumberChapters(chapters)

	// Drop preamble if empty or only noise
	if len(chapters) > 1 && chapters[0].Number == 0 {
		pre := strings.TrimSpace(chapters[0].Content)
		if pre == "" || isNoiseOnly(pre) {
			chapters = chapters[1:]
		}
	}

	return chapters
}

func isNoiseOnly(content string) bool {
	lines := strings.Split(content, "\n")
	meaningful := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || isPreambleLine(t) || isVolumeOnly(t) {
			continue
		}
		meaningful++
	}
	return meaningful < 3
}

func renumberChapters(chapters []ChapterInfo) []ChapterInfo {
	if len(chapters) == 0 {
		return chapters
	}
	startIdx := 0
	if chapters[0].Number == 0 {
		startIdx = 1
	}
	for i := startIdx; i < len(chapters); i++ {
		chapters[i].Number = i - startIdx + 1
	}
	return chapters
}

// ---- Title / Author Detection ----

func DetectNovelMeta(content string) NovelMeta {
	lines := strings.Split(content, "\n")

	// Pattern 1: 《书名》
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if isPreambleLine(t) {
			continue
		}
		if m := regexp.MustCompile(`《([^》]+)》`).FindStringSubmatch(t); len(m) >= 2 {
			return NovelMeta{Title: m[1], Author: extractAuthor(t)}
		}
	}

	// Pattern 2: "书名 作者：作者名"
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if isPreambleLine(t) || len([]rune(t)) > 80 {
			continue
		}
		if strings.Contains(t, "作者") {
			title, author := splitTitleAuthor(t)
			if title != "" {
				return NovelMeta{Title: title, Author: author}
			}
		}
	}

	// Pattern 3: first meaningful line
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || isPreambleLine(t) {
			continue
		}
		if len([]rune(t)) > 50 {
			return NovelMeta{Title: string([]rune(t)[:50]) + "...", Author: ""}
		}
		return NovelMeta{Title: t, Author: ""}
	}
	return NovelMeta{Title: "未命名小说", Author: ""}
}

func DetectNovelTitle(content string) string {
	return DetectNovelMeta(content).Title
}

func splitTitleAuthor(line string) (string, string) {
	for _, sep := range []string{" 作者", "\t作者", "／作者", "/作者", " 作家"} {
		if idx := strings.Index(line, sep); idx > 0 {
			title := strings.TrimSpace(line[:idx])
			rest := strings.TrimSpace(line[idx+len(sep):])
			rest = strings.TrimPrefix(rest, "：")
			rest = strings.TrimPrefix(rest, ":")
			return title, strings.TrimSpace(rest)
		}
	}
	return "", ""
}

func extractAuthor(line string) string {
	for _, sep := range []string{"作者：", "作者:", "作家：", "作家:"} {
		if idx := strings.Index(line, sep); idx >= 0 {
			return strings.TrimSpace(line[idx+len(sep):])
		}
	}
	return ""
}
