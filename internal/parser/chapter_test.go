package parser

import (
	"strconv"
	"strings"
	"testing"
)

func TestParseArabicNumeralChapters(t *testing.T) {
	content := `第1章 穿越
这是第一章的内容，讲述了主角穿越到异世界的经历。
这里还有更多内容，描述了主角的初始状态。

第2章 觉醒
主角觉醒了特殊能力，发现自己的与众不同。

第3章 修炼
主角开始修炼，遇到了各种挑战和机遇。`

	chapters := Parse(content)

	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}
	if chapters[0].Number != 1 || chapters[0].Title != "第1章 穿越" {
		t.Errorf("chapter 0: number=%d title=%q", chapters[0].Number, chapters[0].Title)
	}
	if !strings.Contains(chapters[0].Content, "穿越到异世界") {
		t.Errorf("chapter 0 content mismatch: %s", chapters[0].Content)
	}
	if chapters[1].Number != 2 {
		t.Errorf("chapter 1: expected number 2, got %d", chapters[1].Number)
	}
}

func TestParseChineseNumeralChapters(t *testing.T) {
	content := `第一章 穿越
主角穿越到了异世界，一切从头开始。

第二章 觉醒
在危急关头，主角觉醒了隐藏的血脉之力。

第三章 修炼
为了生存，主角踏上了艰苦的修炼之路。`

	chapters := Parse(content)

	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}
	if chapters[0].Number != 1 {
		t.Errorf("expected chapter 1, got %d", chapters[0].Number)
	}
	if chapters[2].Number != 3 {
		t.Errorf("expected chapter 3, got %d", chapters[2].Number)
	}
}

func TestParseWithPreamble(t *testing.T) {
	content := `《修仙之路》

作者：某大神

简介：这是一部修仙小说，讲述了一个少年从平凡到巅峰的故事。

第一章 开始修炼
修炼的第一天，主角感受到了天地灵气。`

	chapters := Parse(content)

	if len(chapters) < 1 {
		t.Fatal("expected at least 1 chapter")
	}
	hasCh1 := false
	for _, ch := range chapters {
		if ch.Number == 1 {
			hasCh1 = true
			break
		}
	}
	if !hasCh1 {
		t.Error("should have chapter 1")
		t.Logf("chapters: %+v", chapters)
	}
}

func TestParseWithVolumeChapter(t *testing.T) {
	content := `第一卷 第一章 少年
少年时期的往事，埋下了许多伏笔。

第二卷 第五章 成名
主角已名扬天下，但更大的挑战在前方。`

	chapters := Parse(content)

	if len(chapters) < 2 {
		t.Fatalf("expected at least 2 chapters, got %d", len(chapters))
	}
}

func TestParseEmpty(t *testing.T) {
	chapters := Parse("")
	if len(chapters) != 0 {
		t.Errorf("expected 0 chapters, got %d", len(chapters))
	}
}

func TestParseEnglishChapters(t *testing.T) {
	content := `Chapter 1 The Beginning
This is the start of an epic journey across the land.

Chapter 2 The Journey
The journey continues through dark forests and high mountains.`

	chapters := Parse(content)

	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
	if chapters[0].Number != 1 {
		t.Errorf("expected chapter 1, got %d", chapters[0].Number)
	}
}

func TestParseChapterSection(t *testing.T) {
	content := `第1节 开篇
这一节主要介绍了世界背景和基本设定。

第2节 发展
故事开始展开，各方势力逐渐登场。`

	chapters := Parse(content)

	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
}

func TestDetectNovelTitle(t *testing.T) {
	content := `《凡人修仙传》
作者：忘语

第一章 山边小村
一个普通的少年，生活在山边的小村里。`

	title := DetectNovelTitle(content)
	if title != "凡人修仙传" {
		t.Errorf("expected '凡人修仙传', got %q", title)
	}
}

func TestDetectNovelTitleFallback(t *testing.T) {
	content := `这是一个没有书名号的小说标题

第一章 开始
故事从这里开始。`

	title := DetectNovelTitle(content)
	if title == "未命名小说" {
		t.Error("should have detected a fallback title")
	}
}

func TestParseChineseComplexNumerals(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"一", 1},
		{"十", 10},
		{"十一", 11},
		{"二十", 20},
		{"二十五", 25},
		{"一百", 100},
		{"一百一", 101},
		{"一百二十三", 123},
		{"二百五十", 250},
		{"一千零一", 1001},
		{"四十二", 42},
		{"九十九", 99},
	}

	for _, tt := range tests {
		got := parseChineseNumber(tt.input)
		if got != tt.want {
			t.Errorf("parseChineseNumber(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseManyChapters(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		s := strconv.Itoa(i)
		b.WriteString("第")
		b.WriteString(s)
		b.WriteString("章 标题\n")
		b.WriteString("这是第")
		b.WriteString(s)
		b.WriteString("章的内容。\n\n")
	}

	chapters := Parse(b.String())
	if len(chapters) != 50 {
		t.Fatalf("expected 50 chapters, got %d", len(chapters))
	}
	for i, ch := range chapters {
		if ch.Number != i+1 {
			t.Errorf("chapter %d: expected number %d, got %d", i, i+1, ch.Number)
		}
	}
}
