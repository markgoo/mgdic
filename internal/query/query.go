// Package query 把"用户敲进来的那串东西"变成屏幕上那几行字。
//
// 方向判断与降级顺序就是全部内容：
//   - 含汉字 -> 中文方向：贪心切词，每段给几个英文候选
//   - 英文：精确命中 > 词形还原 > 去掉连字符再试 > 当句子逐词 > 前缀提示
//   - 句子永远额外标一句"整句翻译待配置 DeepSeek"，让调用方决定要不要联网
package query

import (
	"sort"
	"strings"

	"mgdic/internal/lexicon"
)

// Searcher 是查询需要的最小接口，store.DB 满足它；测试里可以塞一个假的。
type Searcher interface {
	LookupEN(key string, limit int) ([]lexicon.Entry, error)
	LookupMany(keys []string, limit int) ([]lexicon.Entry, error)
	PrefixEN(key string, limit int) ([]lexicon.Entry, error)
	ZhCandidates(tokens []string, perToken int) (map[string][]lexicon.Entry, error)
}

// Card 是一个词条卡片：界面把每个词条画成一张可点击的卡，点哪张复制哪张。
type Card struct {
	Title string   // 第一行：词头 + 音标
	Lines []string // 释义行
	Meta  string   // 标签/词形（界面上用灰色小字）
	Copy  string   // 点击复制的内容（就是这张卡在屏幕上的文字）
}

// Answer 是一次查询的结果。
//
// Text 是给命令行用的纯文本；Cards/Note 是给界面用的结构化数据——
// 界面要把每个词条画成独立卡片，所以不能只给一整段字符串。
type Answer struct {
	Text     string
	Cards    []Card   // 单词/词组查询：一个词条一张卡
	Chips    []string // 句子查询：主文本切出来的每个词，各占一个小框
	Note     string   // 卡片之外的提示/译文（"按回车翻译"、DeepSeek 结果等）
	NeedsLLM bool
	Kind     string // "en2zh" / "zh2en" / "sentence" / "miss"
}

const (
	maxSenseLines = 3 // 一条词条最多显示几行释义
	zhPerToken    = 1 // 中文方向每段只给最可能的 1 个英文词
	zhFetch       = 8 // 先从库里取这么多个候选，再在 Go 里打分挑最好的
	maxZhToken    = 8 // 中文切词的最大长度（字）
)

// zhCandidateScore 给中文方向的候选打分，用来挑"最可能的那个"。
//
// 词频必须是主导：加分只用来在词频接近时打破平局，不能反过来压过常用词，
// 否则"喜欢"会给出 care for 而不是 like。
func zhCandidateScore(e lexicon.Entry, tok string) int {
	score := e.Freq
	first := lineOne(firstMeaning(e))
	body := strings.TrimSpace(stripPOSPrefix(first))
	switch idx := strings.Index(body, tok); {
	case idx == 0:
		score += 60000 // 释义第一条开头就是它
	case idx > 0 && idx <= 6:
		score += 30000
	case idx > 0:
		score += 10000
	}
	// 缩写词基本不是用户想要的（查"服务器"会冒出 serv 这种）
	if strings.HasPrefix(strings.TrimSpace(first), "abbr.") {
		score -= 120000
	}
	// 变形词降权：查"喜欢"会命中 liked「喜欢（like的过去式）」，用户要的是原形
	if strings.Contains(first, "的过去式") || strings.Contains(first, "的过去分词") ||
		strings.Contains(first, "的复数") || strings.Contains(first, "的现在分词") {
		score -= 100000
	}
	// 词头太长的大多是词组（client/server），降权
	if n := len([]rune(e.Word)); n > 12 {
		score -= 60000
	}
	return score
}

// pickZHCandidate 从候选里挑最可能的那个英文词。
func pickZHCandidate(got []lexicon.Entry, tok string) *lexicon.Entry {
	if len(got) == 0 {
		return nil
	}
	best := 0
	bestScore := zhCandidateScore(got[0], tok)
	for i := 1; i < len(got); i++ {
		if s := zhCandidateScore(got[i], tok); s > bestScore {
			best, bestScore = i, s
		}
	}
	return &got[best]
}

// stripPOSPrefix 去掉 "n." "vt." "[网络]" 这类前缀。
func stripPOSPrefix(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '.'); i > 0 && i <= 4 {
		head := strings.ToLower(s[:i])
		if posPrefixWords[head] {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	if strings.HasPrefix(s, "[") {
		if j := strings.IndexByte(s, ']'); j > 0 && j <= 9 {
			s = strings.TrimSpace(s[j+1:])
		}
	}
	return s
}

var posPrefixWords = map[string]bool{
	"n": true, "v": true, "adj": true, "adv": true, "vt": true, "vi": true,
	"prep": true, "conj": true, "aux": true, "int": true, "interj": true,
	"abbr": true, "suf": true, "pref": true, "phr": true, "art": true,
	"num": true, "pron": true, "a": true, "ad": true,
}

// Query 是查询入口。s 为 nil 时只走"查不到"的分支，方便离线自检。
func Query(s Searcher, q string) Answer {
	q = strings.TrimSpace(q)
	if q == "" {
		return Answer{Kind: "miss"}
	}
	if s == nil {
		return Answer{Text: "词典还没加载。", Kind: "miss", NeedsLLM: true}
	}
	if lexicon.IsHanText(q) {
		return queryZH(s, q)
	}
	return queryEN(s, q)
}

func queryEN(s Searcher, q string) Answer {
	clean := strings.Trim(q, " ,;:?!.,、。")

	// 1) 精确命中优先于一切句子判断
	if a, ok := tryExact(s, clean); ok {
		return a
	}
	// 2) 词形还原：runned -> run，stopping -> stop
	for _, v := range inflectionCandidates(clean) {
		if a, ok := tryExact(s, v); ok {
			return a
		}
	}
	// 3) 词组里的连字符与空格变体：a-lot-of -> a lot of
	if alt := normalizeSpacing(clean); alt != clean {
		if a, ok := tryExact(s, alt); ok {
			return a
		}
	}
	// 4) 拼错一个字母：applicaton -> application
	//    放在句子判断之前，因为句子判断会把任何多词输入都吞掉
	if a, ok := didYouMean(s, clean); ok {
		return a
	}
	// 5) 句子：逐词给命中，整句交给 LLM
	if IsSentence(clean) {
		return renderSentence(s, clean)
	}
	// 6) 前缀提示（拼了一半的词）
	if got, err := s.PrefixEN(lexicon.NormKey(clean), 5); err == nil && len(got) > 0 {
		var b strings.Builder
		b.WriteString("词典里没有 “" + clean + "”。你是想找：")
		for _, e := range got {
			b.WriteString("\n  " + e.Word)
		}
		return Answer{Text: b.String(), Kind: "miss"}
	}
	return Answer{Text: "词典里没有 “" + clean + "”。", Kind: "miss", NeedsLLM: true}
}

// didYouMean 把输入按"编辑距离 1"展开成一批候选键，一次查完再挑最常用的那个。
// 只覆盖单个英文词：词组、带空格的输入不猜，免得把正经句子硬凑成别的词。
func didYouMean(s Searcher, q string) (Answer, bool) {
	if len(q) < 4 || len(q) > 14 || strings.ContainsAny(q, " -'") || !isAlpha(q) {
		return Answer{}, false
	}
	seen := make(map[string]struct{}, 4*len(q))
	keys := make([]string, 0, 4*len(q))
	add := func(v string) {
		if v == q || v == "" {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		keys = append(keys, v)
	}
	// 删一个字母、相邻对调：最常见的两种手误，代价最小先试
	for i := 0; i < len(q); i++ {
		add(q[:i] + q[i+1:])
	}
	for i := 0; i+1 < len(q); i++ {
		b := []byte(q)
		b[i], b[i+1] = b[i+1], b[i]
		add(string(b))
	}
	// 换一个字母、插一个字母：把漏打/多打也覆盖掉
	for i := 0; i < len(q); i++ {
		for c := byte('a'); c <= 'z'; c++ {
			if c != q[i] {
				add(q[:i] + string(c) + q[i+1:])
			}
		}
	}
	for i := 0; i <= len(q); i++ {
		for c := byte('a'); c <= 'z'; c++ {
			add(q[:i] + string(c) + q[i:])
		}
	}

	got, err := s.LookupMany(keys, 8)
	if err != nil || len(got) == 0 {
		return Answer{}, false
	}
	best := rank(got, 1)[0]
	var b strings.Builder
	b.WriteString("词典里没有 “" + q + "”，按 “" + best.Word + "” 给你查：\n")
	b.WriteString(entryText(best))
	return Answer{Text: strings.TrimRight(b.String(), "\n"), Kind: "en2zh"}, true
}

func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func tryExact(s Searcher, word string) (Answer, bool) {
	got, err := s.LookupEN(lexicon.NormKey(word), 3)
	if err != nil || len(got) == 0 {
		return Answer{}, false
	}
	// 每个词条一张卡片。卡片里只放翻译那一侧（中文释义）；
	// 但给命令行看的 Text 要带上词头和音标，否则终端里看不出查的是哪个词。
	cards := make([]Card, 0, len(got))
	var b strings.Builder
	for i, e := range got {
		cards = append(cards, entryCard(e))
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(entryText(e))
	}
	return Answer{Text: strings.TrimRight(b.String(), "\n"), Cards: cards, Kind: "en2zh"}, true
}

// cardText 把一张卡片还原成多行文本（给命令行用）。
func cardText(c Card) string {
	var b strings.Builder
	b.WriteString(c.Title)
	for _, l := range c.Lines {
		b.WriteString("\n  " + l)
	}
	if c.Meta != "" {
		b.WriteString("\n  " + c.Meta)
	}
	return b.String()
}

// entryCard 把一个词条做成卡片：标题行是词头+音标，正文是释义，标签单独一行用灰字。
func entryCard(e lexicon.Entry) Card {
	title := e.Word
	if e.Phonetic != "" {
		title += "  /" + e.Phonetic + "/"
	}
	lines := make([]string, 0, maxSenseLines+1)
	senses := strings.Split(strings.TrimSpace(firstMeaning(e)), "\n")
	for i, l := range senses {
		if i >= maxSenseLines || strings.TrimSpace(l) == "" {
			break
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	if len(senses) > maxSenseLines {
		lines = append(lines, "…")
	}
	meta := metaText(e)
	// 卡片分三层：标题（词头+音标）在框外上方、翻译在圆角框里、标签在框外下方。
	// 只有中间那层进框——复制时也只拿它，因为目的是"查完把翻译贴到别处"。
	cp := strings.Join(lines, "\n")
	return Card{Title: title, Lines: lines, Meta: meta, Copy: cp}
}

// queryZH 处理中文输入：先一次问完所有候选串，再贪心切词。
//
// 贪心是为了解决"我喜欢游泳"这种没有空格的分隔：从每个位置起，取在库里存在的最长词串。
func queryZH(s Searcher, q string) Answer {
	runes := []rune(q)

	// 收集所有可能被问到的候选串（每个位置最多 maxZhToken 个），一次查询问完
	want := make(map[string]struct{}, len(runes)*2)
	for i := 0; i < len(runes); i++ {
		if !lexicon.IsHanRune(runes[i]) {
			continue
		}
		for j := i + 1; j <= i+maxZhToken && j <= len(runes); j++ {
			if !allHan(runes[i:j]) {
				break
			}
			want[string(runes[i:j])] = struct{}{}
		}
	}
	tokens := make([]string, 0, len(want))
	for t := range want {
		tokens = append(tokens, t)
	}
	cands, err := s.ZhCandidates(tokens, zhFetch)
	if err != nil {
		return Answer{Text: "查中文时出错: " + err.Error(), Kind: "miss"}
	}

	type hit struct {
		tok string
		got []lexicon.Entry
	}
	var hits []hit
	for i := 0; i < len(runes); {
		if !lexicon.IsHanRune(runes[i]) {
			i++
			continue
		}
		matched := 0
		for j := min(i+maxZhToken, len(runes)); j > i; j-- {
			tok := string(runes[i:j])
			if got, ok := cands[tok]; ok && len(got) > 0 {
				hits = append(hits, hit{tok: tok, got: got})
				matched = j - i
				break
			}
		}
		if matched == 0 {
			i++ // 这个字不在库里，跳过
		} else {
			i += matched
		}
	}

	if len(hits) == 0 {
		return Answer{
			Text:     "词典里没有 “" + q + "” 的中文词。",
			Note:     "词典里没有 “" + q + "” 的中文词。\n按回车翻译整句",
			Kind:     "miss",
			NeedsLLM: true,
		}
	}
	// 中文也可能是一整句：每个词都查得到，但用户要的是整句翻译。
	// 这时候不要把逐词候选摊出来——中文->英文的单词候选歧义极大，
	// 在句子上就是噪音（"月的"被当成词、"一天"撞上某条网络释义），
	// 用户真正要的是那一句译文。
	if looksLikeSentenceZH(q) {
		// 句子：把切出来的中文词各做一个小框，点哪个复制哪个
		toks := make([]string, 0, len(hits))
		for _, h := range hits {
			toks = append(toks, h.tok)
		}
		return Answer{Text: q, Chips: toks, Note: "按回车翻译整句", Kind: "sentence", NeedsLLM: true}
	}

	// 每段只挑最可能的那个英文词：中文->英文的候选噪音很大
	// （查"苹果"会带出 pomme、app. 这类），列一堆反而没法用。
	multi := len(hits) > 1
	cards := make([]Card, 0, len(hits))
	var b strings.Builder
	for _, h := range hits {
		best := pickZHCandidate(h.got, h.tok)
		if best == nil {
			continue
		}
		if multi {
			b.WriteString(h.tok + "\n")
		}
		// 查中文时"翻译"就是那个英文词——放进框里，点框复制它。
		// 框外下方仍然给出这个英文词的音标、中文释义、考纲标签，
		// 否则结果只剩一个光秃秃的单词，看不出选得对不对。
		c := Card{Lines: []string{best.Word}, Copy: best.Word}
		var meta []string
		if best.Phonetic != "" {
			meta = append(meta, "/"+best.Phonetic+"/")
		}
		meta = append(meta, lineOne(firstMeaning(*best)))
		if m := metaText(*best); m != "" {
			meta = append(meta, m)
		}
		if multi {
			meta = append(meta, "来自「"+h.tok+"」")
		}
		c.Meta = strings.Join(meta, " · ")
		cards = append(cards, c)
		b.WriteString("  " + zhLine(*best) + "\n")
	}
	return Answer{Text: strings.TrimRight(b.String(), "\n"), Cards: cards, Kind: "zh2en"}
}

// looksLikeSentenceZH 判断中文输入是不是"一句话"而不是"一个词"。
// 五个字以上、或带句读标点，就认为用户想要整句翻译；两三个字的词条查询不受影响。
func looksLikeSentenceZH(q string) bool {
	if strings.ContainsAny(q, "。？！，、；：…—") {
		return true
	}
	return len([]rune(strings.TrimSpace(q))) >= 5
}

func allHan(r []rune) bool {
	for _, c := range r {
		if !lexicon.IsHanRune(c) {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renderSentence 逐词给命中，整句翻译交给 LLM。
func renderSentence(s Searcher, q string) Answer {
	var b strings.Builder
	var chips []string
	b.WriteString(q + "\n")
	any := false
	for _, w := range strings.Fields(q) {
		lo := strings.ToLower(strings.Trim(w, ",.?!:;\"'()"))
		if lo == "" {
			continue
		}
		got, err := s.LookupEN(lexicon.NormKey(lo), 3)
		if err != nil {
			continue
		}
		if len(got) == 0 {
			for _, v := range inflectionCandidates(lo) {
				if g, err := s.LookupEN(lexicon.NormKey(v), 3); err == nil && len(g) > 0 {
					got = g
					break
				}
			}
		}
		if len(got) == 0 {
			continue
		}
		any = true
		// 同一个词常有多条释义，句子逐词展示时挑最常用的那条
		best := rank(got, 1)[0]
		line := lineOne(firstMeaning(best))
		b.WriteString("  " + lo + " → " + line + "\n")
		chips = append(chips, lo)
	}
	if !any {
		return Answer{Text: q, Note: "按回车翻译整句", Kind: "miss", NeedsLLM: true}
	}
	// 句子：把主文本切成一个个词，界面上每个词一个小框，点哪个复制哪个。
	// 逐词释义仍然写进 Text 给命令行看。
	return Answer{
		Text:     strings.TrimRight(b.String(), "\n"),
		Chips:    chips,
		Note:     "按回车翻译整句",
		Kind:     "sentence",
		NeedsLLM: true,
	}
}

// IsSentence 粗略判断是不是句子：三个词以上、或够长，就当句子。
// 精确命中永远先试过，所以这里放宽一点是安全的（词组"a lot of"会先被词典接住）。
func IsSentence(s string) bool {
	f := strings.Fields(s)
	if len(f) >= 3 {
		return true
	}
	if len(f) == 0 {
		return false
	}
	return lexicon.RuneLen(s) >= 20
}

func normalizeSpacing(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "-", " ")), " ")
}

// entryText 是一条词条的显示文本：词头+音标一行，释义缩进两格，最后一行是标签与词形。
// 缩进只用两个空格：结果区是比例字体，四个空格会显得过宽、层级反而看不清。
func entryText(e lexicon.Entry) string {
	var b strings.Builder
	b.WriteString(e.Word)
	if e.Phonetic != "" {
		b.WriteString("  /" + e.Phonetic + "/")
	}
	b.WriteString("\n")

	lines := strings.Split(strings.TrimSpace(firstMeaning(e)), "\n")
	for i, l := range lines {
		if i >= maxSenseLines || strings.TrimSpace(l) == "" {
			break
		}
		b.WriteString("  " + strings.TrimSpace(l) + "\n")
	}
	if len(lines) > maxSenseLines {
		b.WriteString("  …\n")
	}
	if meta := metaText(e); meta != "" {
		b.WriteString("  " + meta + "\n")
	}
	return b.String()
}

// firstMeaning 优先用中文释义，没有中文就用英文释义兜底。
func firstMeaning(e lexicon.Entry) string {
	if strings.TrimSpace(e.Translation) != "" {
		return e.Translation
	}
	return e.Definition
}

// zhLine 是中文方向的一条候选：英文词（带音标）+ 释义第一行，释义截一刀别拖成横幅。
func zhLine(e lexicon.Entry) string {
	s := e.Word
	if e.Phonetic != "" {
		s += "  /" + e.Phonetic + "/"
	}
	m := lineOne(firstMeaning(e))
	if r := []rune(m); len(r) > 40 {
		m = string(r[:40]) + "…"
	}
	if strings.TrimSpace(m) != "" {
		s += "  " + strings.TrimSpace(m)
	}
	return s
}

func lineOne(s string) string {
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func rank(entries []lexicon.Entry, n int) []lexicon.Entry {
	cp := append([]lexicon.Entry(nil), entries...)
	sort.SliceStable(cp, func(a, b int) bool { return cp[a].Freq > cp[b].Freq })
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp
}

// metaText 拼一行辅助信息：考纲标签、牛津词、常用度、词形变化。
func metaText(e lexicon.Entry) string {
	var bits []string
	if e.Tag != "" {
		bits = append(bits, "考纲 "+strings.Join(strings.Fields(e.Tag), " "))
	}
	if e.Oxford {
		bits = append(bits, "牛津3000")
	}
	if e.Freq >= 900000 {
		bits = append(bits, "常用")
	}
	if e.Exchange != "" {
		bits = append(bits, "词形 "+exchangeText(e.Exchange))
	}
	return strings.Join(bits, " · ")
}

// exchangeLabels 是 ECDICT README 里那张表，原文照搬：
// p 过去式 / d 过去分词 / i 现在分词 / 3 第三人称单数 / r 比较级 / t 最高级 / s 名词复数 /
// 0 Lemma（原形）/ 1 Lemma 的变换形式。分隔符是斜杠，不是竖线。
var exchangeLabels = map[string]string{
	"p": "过去式", "d": "过去分词", "i": "现在分词", "3": "三单",
	"r": "比较级", "t": "最高级", "s": "复数", "0": "原形", "1": "原形变换",
}

func exchangeText(s string) string {
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		k := strings.TrimSpace(p)
		kv := strings.SplitN(k, ":", 2)
		if len(kv) != 2 {
			out = append(out, k)
			continue
		}
		leg, val := strings.ToLower(strings.TrimSpace(kv[0])), strings.TrimSpace(kv[1])
		switch leg {
		case "0": // 0 的值是原形单词本身
			out = append(out, "原形 "+val)
		case "1": // 1 的值是字母：说明本词条是原形的哪种变形
			out = append(out, "本词条是原形的"+labelList(val))
		default:
			if label, ok := exchangeLabels[leg]; ok {
				out = append(out, label+" "+val)
			} else {
				out = append(out, k)
			}
		}
	}
	return strings.Join(out, "、")
}

func labelList(val string) string {
	bits := make([]string, 0, len(val))
	for _, c := range strings.ToLower(val) {
		if label, ok := exchangeLabels[string(c)]; ok {
			bits = append(bits, label)
			continue
		}
		bits = append(bits, string(c))
	}
	return strings.Join(bits, "、")
}

// trimDoubled 去掉词尾双写的辅音：runn 变 run，stopp 变 stop。
// 它只是候选不是结论（passed 的 stem pass 本身就是双写 s），所以调用方连着 stem 一起试。
// ECDICT 自带的 lemma.en.txt 才是首选来源，这套规则是查不到时的兜底。
func trimDoubled(stem string) (string, bool) {
	if len(stem) < 3 {
		return stem, false
	}
	a, b := stem[len(stem)-1], stem[len(stem)-2]
	if a != b {
		return stem, false
	}
	switch a {
	case 'b', 'p', 'd', 'g', 'l', 'n', 'r', 't', 'm', 's', 'w':
		return stem[:len(stem)-1], true
	}
	return stem, false
}

// inflectionCandidates 给出常见的英语词形还原候选，按可能性排序。
func inflectionCandidates(q string) []string {
	if len(q) < 3 || strings.ContainsAny(q, " ") {
		return nil
	}
	var out []string
	add := func(s string) {
		if s != q && !strings.ContainsAny(s, " ") {
			out = append(out, s)
		}
	}
	switch {
	case strings.HasSuffix(q, "ies") && len(q) > 4:
		add(strings.TrimSuffix(q, "ies") + "y")
		add(strings.TrimSuffix(q, "ies") + "ie")
	case strings.HasSuffix(q, "ied") && len(q) > 4:
		add(strings.TrimSuffix(q, "ied") + "y")
		add(strings.TrimSuffix(q, "ied") + "ie")
	case strings.HasSuffix(q, "ing") && len(q) > 4:
		stem := strings.TrimSuffix(q, "ing")
		add(stem)
		add(stem + "e")
		add(stem + "s")
		if s, ok := trimDoubled(stem); ok {
			add(s)
			add(s + "e")
		}
	case strings.HasSuffix(q, "ed") && len(q) > 3:
		stem := strings.TrimSuffix(q, "ed")
		add(stem)
		add(stem + "e")
		add(stem + "i")
		if s, ok := trimDoubled(stem); ok {
			add(s)
			add(s + "e")
		}
		add(stem + "ie")
	}
	switch {
	case strings.HasSuffix(q, "es") && len(q) > 3:
		add(strings.TrimSuffix(q, "es"))
		add(strings.TrimSuffix(q, "es") + "e")
		add(strings.TrimSuffix(q, "es") + "s")
	case strings.HasSuffix(q, "s") && len(q) > 3:
		add(strings.TrimSuffix(q, "s"))
		add(strings.TrimSuffix(q, "s") + "s")
		add(strings.TrimSuffix(q, "s") + "e")
	}
	// 去重，保持顺序
	seen := make(map[string]bool, len(out))
	filtered := out[:0]
	for _, s := range out {
		if !seen[s] {
			seen[s] = true
			filtered = append(filtered, s)
		}
	}
	return filtered
}
