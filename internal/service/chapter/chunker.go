package chapter

import (
	"regexp"
	"strings"
	"unicode"
)

// Chunk represents a text segment with its position in the source.
type Chunk struct {
	Content   string // 块文本
	CharStart int    // 在原文中的起始字符位置
	CharEnd   int    // 在原文中的结束字符位置
}

const (
	defaultMaxChars       = 400 // BGE 模型 512 token 限制，保守用 400 字
	defaultOverlapSents   = 2   // 相邻 chunk 重叠句子数
	longSentenceThreshold = 400 // 单句超过此长度视为超长句
)

// sentenceSplitRe matches Chinese sentence-ending punctuation.
// 。！？… — standard sentence enders
// ； — clause boundary (weaker, used for long-sentence splitting)
// "） — closing quotes/parens often precede a real sentence break
var sentenceSplitRe = regexp.MustCompile(`[^。！？…\n]+[。！？…]?`)

// longSentenceSplitRe for breaking sentences that exceed the chunk limit.
var longSentenceSplitRe = regexp.MustCompile(`[^，；：]+[，；：]?`)

// ChunkContent splits chapter content into overlapping chunks at sentence boundaries.
//
// Strategy:
//  1. Split into sentences by Chinese punctuation (。！？…\n)
//  2. Greedily merge sentences until approaching maxChars
//  3. Overlap: each chunk shares its last overlapSents sentences with the next
//  4. Paragraph boundary (\n\n) is treated as a preferred split point
//  5. Long sentences (>maxChars) are split at weaker punctuation (，；：)
func ChunkContent(content string) []Chunk {
	return ChunkContentWithParams(content, defaultMaxChars, defaultOverlapSents)
}

// ChunkContentWithParams is the configurable version used by tests.
func ChunkContentWithParams(content string, maxChars, overlapSents int) []Chunk {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	sentences := splitSentences(content)
	if len(sentences) == 0 {
		return nil
	}

	var chunks []Chunk
	var buf []chunkSentence
	bufRunes := 0
	charPos := 0 // current position in the full rune slice

	for i := 0; i < len(sentences); i++ {
		s := sentences[i]
		sRunes := len([]rune(s))

		// Handle sentences longer than maxChars: split at weak punctuation
		if sRunes > maxChars {
			// Flush current buffer first
			if len(buf) > 0 {
				chunks = append(chunks, buildChunk(buf, charPos, overlapSents))
				charPos = buf[len(buf)-overlapSents].charStart
				buf = buf[len(buf)-overlapSents:]
				bufRunes = runeSum(buf)
			}

			subChunks := splitLongSentence(s, maxChars, charPos)
			chunks = append(chunks, subChunks...)

			last := subChunks[len(subChunks)-1]
			charPos = last.CharEnd
			buf = nil
			bufRunes = 0
			continue
		}

		// Check if adding this sentence would exceed maxChars
		if bufRunes+sRunes > maxChars && len(buf) > 0 {
			chunks = append(chunks, buildChunk(buf, charPos, overlapSents))

			// Keep last overlapSents sentences as overlap, reset position
			overlapStart := max(0, len(buf)-overlapSents)
			charPos = buf[overlapStart].charStart
			buf = buf[overlapStart:]
			bufRunes = runeSum(buf)
		}

		buf = append(buf, chunkSentence{
			text:      s,
			charStart: charPos + runeOffset(content, charPos, s),
			charEnd:   charPos + runeOffset(content, charPos, s) + sRunes,
		})
		charPos = buf[0].charStart // keep tracking from first in buffer
		bufRunes += sRunes

		// Paragraph boundary: if sentence ends with \n\n, flush current buffer
		if strings.HasSuffix(s, "\n\n") || strings.HasSuffix(s, "\n") {
			// Only flush if we have enough content (avoid tiny chunks from dialogue)
			if bufRunes >= maxChars/2 {
				chunks = append(chunks, buildChunk(buf, charPos, overlapSents))
				overlapStart := max(0, len(buf)-overlapSents)
				charPos = buf[overlapStart].charStart
				buf = buf[overlapStart:]
				bufRunes = runeSum(buf)
			}
		}
	}

	// Flush remaining buffer
	if len(buf) > 0 {
		chunks = append(chunks, buildChunk(buf, charPos, overlapSents))
	}

	// Recalculate accurate char positions
	fixCharPositions(content, chunks)

	return chunks
}

// chunkSentence tracks a sentence with its position in the source.
type chunkSentence struct {
	text      string
	charStart int
	charEnd   int
}

// splitSentences splits text at Chinese sentence-ending punctuation.
func splitSentences(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	raw := sentenceSplitRe.FindAllString(text, -1)
	if len(raw) == 0 {
		return []string{text}
	}

	// Merge any trailing whitespace/newlines that the regex didn't capture
	var result []string
	for i := 0; i < len(raw); i++ {
		s := raw[i]
		// If not the last sentence, trim trailing whitespace
		if i < len(raw)-1 {
			s = strings.TrimRight(s, " \t")
		}
		if strings.TrimSpace(s) != "" {
			result = append(result, s)
		}
	}
	return result
}

// splitLongSentence breaks a sentence that exceeds maxChars at weak punctuation boundaries.
func splitLongSentence(sentence string, maxChars int, basePos int) []Chunk {
	clauses := longSentenceSplitRe.FindAllString(sentence, -1)
	if len(clauses) == 0 {
		// Fallback: hard cut at maxChars
		runes := []rune(sentence)
		for start := 0; start < len(runes); start += maxChars {
			end := start + maxChars
			if end > len(runes) {
				end = len(runes)
			}
			return []Chunk{{
				Content:   string(runes[start:end]),
				CharStart: basePos + start,
				CharEnd:   basePos + end,
			}}
		}
	}

	var chunks []Chunk
	var buf []string
	bufLen := 0
	clauseStart := basePos

	for _, clause := range clauses {
		cLen := len([]rune(clause))
		if bufLen+cLen > maxChars && len(buf) > 0 {
			content := strings.Join(buf, "")
			chunks = append(chunks, Chunk{
				Content:   content,
				CharStart: clauseStart,
				CharEnd:   clauseStart + len([]rune(content)),
			})
			clauseStart += len([]rune(buf[0]))
			buf = buf[1:] // overlap by 1 clause
			bufLen = runeStrSum(buf)
		}
		buf = append(buf, clause)
		bufLen += cLen
	}

	if len(buf) > 0 {
		content := strings.Join(buf, "")
		chunks = append(chunks, Chunk{
			Content:   content,
			CharStart: clauseStart,
			CharEnd:   clauseStart + len([]rune(content)),
		})
	}

	return chunks
}

// buildChunk creates a Chunk from a buffer of sentences.
func buildChunk(buf []chunkSentence, basePos int, overlapSents int) Chunk {
	var sb strings.Builder
	for _, s := range buf {
		sb.WriteString(s.text)
	}
	content := sb.String()
	return Chunk{
		Content:   content,
		CharStart: buf[0].charStart,
		CharEnd:   buf[len(buf)-1].charEnd,
	}
}

// runeSum returns the total rune count of a sentence buffer.
func runeSum(buf []chunkSentence) int {
	total := 0
	for _, s := range buf {
		total += len([]rune(s.text))
	}
	return total
}

// runeStrSum returns the total rune count of a string slice.
func runeStrSum(ss []string) int {
	total := 0
	for _, s := range ss {
		total += len([]rune(s))
	}
	return total
}

// runeOffset finds the position of a substring within text.
func runeOffset(text string, hint int, substr string) int {
	if substr == "" {
		return hint
	}
	runes := []rune(text)
	subRunes := []rune(substr)

	// Search forward from hint
	for i := hint; i <= len(runes)-len(subRunes); i++ {
		match := true
		for j := 0; j < len(subRunes); j++ {
			if runes[i+j] != subRunes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return hint
}

// fixCharPositions recalculates CharStart/CharEnd for all chunks by scanning the source text.
func fixCharPositions(content string, chunks []Chunk) {
	runes := []rune(content)
	for i := range chunks {
		chunkRunes := []rune(chunks[i].Content)
		if len(chunkRunes) == 0 {
			continue
		}

		// Search for this chunk's content in the full text
		pos := findRuneSequence(runes, chunkRunes, 0)
		if pos >= 0 {
			chunks[i].CharStart = pos
			chunks[i].CharEnd = pos + len(chunkRunes)
		}
	}
}

// findRuneSequence finds the position of needle in haystack, searching from start.
func findRuneSequence(haystack, needle []rune, start int) int {
	if len(needle) == 0 {
		return start
	}
	for i := start; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// isCJKPunct reports whether r is a CJK punctuation mark used for sentence boundaries.
func isCJKPunct(r rune) bool {
	switch r {
	case '。', '！', '？', '…', '；', '：', '，', '、':
		return true
	}
	return false
}

// Ensure unicode import is used.
var _ = unicode.Is
