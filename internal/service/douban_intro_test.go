package service

import (
	"strings"
	"testing"
)

// 豆瓣条目页 link-report 区块的真实结构（简化）：
// 同一 property="v:summary" 有两个 span，class="short" 为截断版，另一个为全文。
const sampleSubjectPage = `<!DOCTYPE html>
<html>
<body>
<div id="link-report">
	<span property="v:summary" class="short"> 这是截断版简介，只有第一段… </span>
	<span property="v:summary"> 完整版<b>第一段</b>，包含加粗标签。<br><br>　　　第二段，带
	换行与 &amp; 实体、&ldquo;中文引号&rdquo;、&hellip; 省略号。<br>(©豆瓣)</span>
</div>
</body>
</html>`

func TestExtractSummaryPrefersFullVersion(t *testing.T) {
	got := extractSummary(sampleSubjectPage)

	if strings.Contains(got, "截断版") {
		t.Errorf("应跳过 class=short 的截断版，实际拿到: %q", got)
	}
	if !strings.Contains(got, "完整版第一段，包含加粗标签。") {
		t.Errorf("简介应含全文首段（标签已剥离）, 实际: %q", got)
	}
	if !strings.Contains(got, "第二段，带\n换行与 & 实体、“中文引号”、… 省略号。") {
		t.Errorf("简介应含次段（实体已反转义）, 实际: %q", got)
	}
	if strings.Contains(got, "©豆瓣") {
		t.Errorf("应去掉 (©豆瓣) 后缀, 实际: %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("不应残留 HTML 标签, 实际: %q", got)
	}
	if !strings.Contains(got, "\n") {
		t.Errorf("段落间应保留换行, 实际: %q", got)
	}
}

func TestExtractSummaryEmptyWhenNoSummary(t *testing.T) {
	if got := extractSummary("<html><body>没有简介的页面</body></html>"); got != "" {
		t.Errorf("无 v:summary 时应返回空串, 实际: %q", got)
	}
}

func TestExtractSummaryFallbackToLastShort(t *testing.T) {
	// 个别页面只有截断版：取最后一个（最完整）
	html := `<span property="v:summary" class="short">短版本简介。</span>`
	if got := extractSummary(html); got != "短版本简介。" {
		t.Errorf("仅有 short 版时应取其内容, 实际: %q", got)
	}
}

func TestCleanSummaryNormalizesWhitespace(t *testing.T) {
	got := cleanSummary("  第一段 <br/><br/><br/> 第二段  ")
	want := "第一段\n\n第二段"
	if got != want {
		t.Errorf("清洗结果不符, got %q, want %q", got, want)
	}
}
