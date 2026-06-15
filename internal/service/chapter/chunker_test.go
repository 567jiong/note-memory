package chapter

import (
	"strings"
	"testing"
)

// ---- splitSentences tests ----

func TestSplitSentences_Simple(t *testing.T) {
	text := "韩立推开石门。眼前豁然开朗。洞府中央悬浮着一尊青铜古灯。"
	sents := splitSentences(text)
	if len(sents) != 3 {
		t.Fatalf("expected 3 sentences, got %d: %v", len(sents), sents)
	}
	if !strings.Contains(sents[0], "推开石门") {
		t.Errorf("sentence 1 mismatch: %s", sents[0])
	}
	if !strings.Contains(sents[1], "豁然开朗") {
		t.Errorf("sentence 2 mismatch: %s", sents[1])
	}
}

func TestSplitSentences_WithExclamation(t *testing.T) {
	text := "不好！被发现了！韩立脸色一变。"
	sents := splitSentences(text)
	if len(sents) < 2 {
		t.Fatalf("expected at least 2 sentences, got %d: %v", len(sents), sents)
	}
}

func TestSplitSentences_WithQuestion(t *testing.T) {
	text := "这是什么法宝？韩立心中疑惑。难道是真品？"
	sents := splitSentences(text)
	if len(sents) < 2 {
		t.Fatalf("expected at least 2 sentences, got %d: %v", len(sents), sents)
	}
	t.Logf("sentences: %v", sents)
}

func TestSplitSentences_Empty(t *testing.T) {
	sents := splitSentences("")
	if len(sents) != 0 {
		t.Errorf("expected 0 sentences for empty text, got %d", len(sents))
	}
}

func TestSplitSentences_NoSentenceMarkers(t *testing.T) {
	text := "这是一段没有标点的纯文本"
	sents := splitSentences(text)
	if len(sents) != 1 {
		t.Errorf("expected 1 sentence, got %d", len(sents))
	}
}

// ---- ChunkContent tests ----

func TestChunkContent_SmallParagraph(t *testing.T) {
	// Content that fits in one chunk
	content := "韩立推开石门。眼前豁然开朗。洞府中央悬浮着一尊青铜古灯。"
	chunks := ChunkContentWithParams(content, 400, 2)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "青铜古灯") {
		t.Errorf("chunk missing content: %s", chunks[0].Content)
	}
}

func TestChunkContent_Overlap(t *testing.T) {
	// Generate enough content to force 2 chunks with overlap
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("第" + strings.Repeat("章", i%5+1) + "段内容描述了修仙世界中的各种奇遇和冒险。")
		sb.WriteString("主角获得了新的法宝和功法传承。")
		sb.WriteString("他与敌人展开了一场激烈的战斗。")
	}
	content := sb.String()

	chunks := ChunkContentWithParams(content, 100, 2)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks with maxChars=100, got %d", len(chunks))
	}

	// Check overlap: last sentence of chunk 0 should appear in chunk 1
	lastSent0 := splitSentences(chunks[0].Content)
	firstSent1 := splitSentences(chunks[1].Content)

	if len(lastSent0) < 2 || len(firstSent1) < 2 {
		t.Skip("not enough sentences for overlap test")
	}

	// The overlap should mean some content appears in both
	overlapFound := false
	for _, s0 := range lastSent0[len(lastSent0)-2:] {
		for _, s1 := range firstSent1[:2] {
			if strings.TrimSpace(s0) == strings.TrimSpace(s1) {
				overlapFound = true
			}
		}
	}
	if !overlapFound {
		t.Log("chunk 0 end:", lastSent0[len(lastSent0)-2:])
		t.Log("chunk 1 start:", firstSent1[:2])
		t.Error("expected overlap sentences between chunks")
	}
}

func TestChunkContent_CharPositions(t *testing.T) {
	content := "第一段内容。第二段内容。第三段内容。第四段内容。第五段内容。"
	chunks := ChunkContentWithParams(content, 30, 1)
	for i, ck := range chunks {
		if ck.CharStart < 0 || ck.CharEnd > len([]rune(content)) {
			t.Errorf("chunk %d: invalid char range [%d, %d] for content of %d runes",
				i, ck.CharStart, ck.CharEnd, len([]rune(content)))
		}
		if ck.CharStart >= ck.CharEnd && len(chunks) > 1 {
			t.Errorf("chunk %d: empty range [%d, %d]", i, ck.CharStart, ck.CharEnd)
		}
	}
}

func TestChunkContent_Empty(t *testing.T) {
	chunks := ChunkContent("")
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty content, got %d", len(chunks))
	}
}

func TestChunkContent_SingleSentence(t *testing.T) {
	chunks := ChunkContent("单独一句话没有标点结尾")
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for single sentence, got %d", len(chunks))
	}
}

func TestChunkContent_ChineseNovelStyle(t *testing.T) {
	content := `韩立站在血色禁地边缘，目光凝重地望着前方翻涌的血雾。

这片禁地乃上古修士遗留，每隔三百年开启一次。传闻其中藏有元婴后期大能的
完整传承，以及数不尽的天材地宝。但危险同样致命——禁地中遍布空间裂缝与
上古禁制，稍有不慎便是形神俱灭。

"终于等到这一天了。"韩立深吸一口气，手中紧握着那枚从拍卖会上得来的
血纹令牌。此令牌是进入禁地的唯一凭证，他为此几乎耗尽了全部身家。

身旁的吕师兄低声道："韩师弟，禁地之中凶险异常，我等筑基修士在其中如同
蝼蚁。你真的想好了？"

韩立点了点头，眼中闪过一丝坚定："修仙之路，不进则退。若连这点风险都不敢
承担，谈何大道？"

说罢，他身形一动，化作一道青虹射入血雾之中。

进入禁地的瞬间，一股浓郁到极致的灵气扑面而来。韩立甚至能感觉到自己的
修为瓶颈在微微松动。然而还不等他欣喜，一道血色的空间裂缝便毫无征兆地
出现在他身前三尺之处！

"不好！"韩立瞳孔骤缩，浑身灵力狂涌，硬生生将身形向左侧横移了半丈。

空间裂缝擦着他的右臂掠过，衣袖瞬间化为虚无。冷汗顺着额角滑落——若是
慢上半分，失去的就是整条手臂。`

	chunks := ChunkContentWithParams(content, 200, 2)
	if len(chunks) == 0 {
		t.Fatal("expected chunks for novel content")
	}

	// Each chunk should be under 200 + some tolerance
	for i, ck := range chunks {
		ckLen := len([]rune(ck.Content))
		if ckLen > 250 {
			t.Errorf("chunk %d: length %d exceeds limit (200 + 50 tolerance)", i, ckLen)
		}
	}

	// Content should be recoverable (at least the first and last sentences)
	if len(chunks) >= 1 {
		if !strings.Contains(chunks[0].Content, "韩立站在血色禁地边缘") {
			t.Error("first chunk missing beginning of content")
		}
		lastChunk := chunks[len(chunks)-1]
		if !strings.Contains(lastChunk.Content, "整条手臂") {
			t.Error("last chunk missing end of content")
		}
	}
}

func TestChunkContent_ContentRecovery(t *testing.T) {
	// Verify that all original sentences appear in at least one chunk
	content := "第一句。第二句。第三句。第四句。第五句。第六句。第七句。第八句。"
	chunks := ChunkContentWithParams(content, 20, 2)

	// Build set of all sentences in all chunks
	chunkSents := make(map[string]bool)
	for _, ck := range chunks {
		for _, s := range splitSentences(ck.Content) {
			chunkSents[strings.TrimSpace(s)] = true
		}
	}

	// Each original sentence should appear in at least one chunk
	origSents := splitSentences(content)
	for _, s := range origSents {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !chunkSents[s] {
			t.Errorf("original sentence %q not found in any chunk", s)
		}
	}
}

// ---- Long sentence splitting tests ----

func TestSplitLongSentence(t *testing.T) {
	// Build a sentence longer than maxChars with commas
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("这是一段很长的描述文字，包含了许多细节和修饰语，")
	}
	sentence := sb.String()

	chunks := splitLongSentence(sentence, 100, 0)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for long sentence (len=%d), got %d",
			len([]rune(sentence)), len(chunks))
	}
	for i, ck := range chunks {
		if len([]rune(ck.Content)) > 150 {
			t.Errorf("chunk %d: length %d exceeds limit", i, len([]rune(ck.Content)))
		}
	}
}

func TestSplitLongSentence_Short(t *testing.T) {
	short := "这是一个短句。"
	chunks := splitLongSentence(short, 400, 0)
	if len(chunks) < 1 {
		t.Error("expected at least 1 chunk for short sentence")
	}
}
