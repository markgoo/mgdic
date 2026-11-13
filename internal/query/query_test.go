package query

import (
	"strings"
	"testing"

	"mgdic/internal/lexicon"
)

// fake 是一个内存版的 Searcher：够小，能把每种降级路径单独钉住。
type fake struct {
	exact map[string][]lexicon.Entry
	zh    map[string][]lexicon.Entry
	pre   map[string][]lexicon.Entry
}

func (f *fake) LookupEN(key string, limit int) ([]lexicon.Entry, error) {
	return f.exact[key], nil
}

func (f *fake) LookupMany(keys []string, limit int) ([]lexicon.Entry, error) {
	var out []lexicon.Entry
	for _, k := range keys {
		out = append(out, f.exact[k]...)
	}
	return out, nil
}

func (f *fake) PrefixEN(key string, limit int) ([]lexicon.Entry, error) {
	return f.pre[key], nil
}

func (f *fake) ZhCandidates(tokens []string, perToken int) (map[string][]lexicon.Entry, error) {
	out := map[string][]lexicon.Entry{}
	for _, t := range tokens {
		if got, ok := f.zh[t]; ok {
			out[t] = got
		}
	}
	return out, nil
}

func newFake() *fake {
	return &fake{
		exact: map[string][]lexicon.Entry{
			"run": {{
				Word: "run", Phonetic: "rʌn", Translation: "v. 跑, 奔跑",
				Freq: 900000, Oxford: true, Exchange: "p:ran/i:running/0:run",
			}},
			"application": {{Word: "application", Translation: "n. 应用, 申请", Freq: 700000}},
			"a lot of":    {{Word: "a lot of", Translation: "许多的", Freq: 300000}},
			"apple":       {{Word: "apple", Phonetic: "'æpl", Translation: "n. 苹果", Freq: 800000, Oxford: true, Tag: "zk gk"}},
			// 句子逐词查要用的三个词
			"i":        {{Word: "i", Translation: "pron. 我", Freq: 990000}},
			"like":     {{Word: "like", Translation: "vt. 喜欢", Freq: 900000}},
			"swimming": {{Word: "swimming", Translation: "n. 游泳", Freq: 400000}},
		},
		zh: map[string][]lexicon.Entry{
			"游泳": {{Word: "swim", Translation: "n. 游泳", Freq: 600000}},
			"喜欢": {{Word: "like", Translation: "vt. 喜欢", Freq: 700000}},
			"我":  {{Word: "I", Translation: "pron. 我", Freq: 900000}},
		},
		pre: map[string][]lexicon.Entry{
			"appl": {{Word: "apple"}, {Word: "apply"}},
		},
	}
}

func TestExactEnglish(t *testing.T) {
	a := Query(newFake(), "apple")
	if a.Kind != "en2zh" {
		t.Fatalf("Kind = %q, 想要 en2zh", a.Kind)
	}
	for _, want := range []string{"apple", "/'æpl/", "n. 苹果", "牛津3000", "考纲 zk gk"} {
		if !strings.Contains(a.Text, want) {
			t.Errorf("输出里缺 %q：\n%s", want, a.Text)
		}
	}
}

func TestPhraseBeforeSentence(t *testing.T) {
	// "a lot of" 是三个词，但词典里有它，必须先命中词组而不是当句子逐词拆
	a := Query(newFake(), "a lot of")
	if a.Kind != "en2zh" {
		t.Fatalf("Kind = %q，词组应当优先于句子判断", a.Kind)
	}
	if !strings.Contains(a.Text, "许多的") {
		t.Errorf("没有给出词组释义：\n%s", a.Text)
	}
}

func TestInflectionFallback(t *testing.T) {
	// runned 不在词典里，要还原成 run
	a := Query(newFake(), "runned")
	if a.Kind != "en2zh" || !strings.Contains(a.Text, "run") {
		t.Fatalf("词形还原失败：Kind=%q\n%s", a.Kind, a.Text)
	}
}

func TestTypoSuggestion(t *testing.T) {
	a := Query(newFake(), "applicaton") // 少一个 i
	if a.Kind != "en2zh" || !strings.Contains(a.Text, "application") {
		t.Fatalf("拼写纠错失败：Kind=%q\n%s", a.Kind, a.Text)
	}
	if !strings.Contains(a.Text, "按") {
		t.Errorf("应当说明是按哪个词查的：\n%s", a.Text)
	}
}

func TestChineseGreedySplit(t *testing.T) {
	// 没有空格的中文要能切开：我喜欢 -> 我 / 喜欢
	a := Query(newFake(), "我喜欢")
	if a.Kind != "zh2en" {
		t.Fatalf("Kind = %q，想要 zh2en", a.Kind)
	}
	for _, want := range []string{"我", "喜欢", "like"} {
		if !strings.Contains(a.Text, want) {
			t.Errorf("输出里缺 %q：\n%s", want, a.Text)
		}
	}
}

// 中文也可能是一整句：这时候只给译文，不要把逐词候选摊出来。
// 中文->英文的单词候选在句子上就是噪音（"月的"被当成词、"一天"撞上网络释义）。
func TestChineseSentenceNeedsLLM(t *testing.T) {
	a := Query(newFake(), "我喜欢游泳")
	if !a.NeedsLLM {
		t.Errorf("中文句子应当标记 NeedsLLM，否则永远不翻整句")
	}
	if a.Kind != "sentence" {
		t.Errorf("Kind = %q，想要 sentence", a.Kind)
	}
	if a.Text != "我喜欢游泳" {
		t.Errorf("中文句子不该附带逐词候选，Text 应为原文，实际：\n%s", a.Text)
	}
	// 短词条不该被当成句子
	if b := Query(newFake(), "游泳"); b.NeedsLLM || b.Kind != "zh2en" {
		t.Errorf("“游泳”是词条查询，不该触发翻译：Kind=%q NeedsLLM=%v", b.Kind, b.NeedsLLM)
	}
}

func TestSentenceNeedsLLM(t *testing.T) {
	a := Query(newFake(), "i like swimming")
	if !a.NeedsLLM {
		t.Fatalf("句子应当标记 NeedsLLM")
	}
	if a.Kind != "sentence" {
		t.Errorf("Kind = %q，想要 sentence", a.Kind)
	}
	// 英文句子的逐词释义要留着（有用），但不该再塞"待配置"这类提示：
	// 译文回来时界面要替换掉它，否则会和真译文一起留在屏幕上
	if !strings.Contains(a.Text, "swimming") {
		t.Errorf("英文句子应当保留逐词释义：\n%s", a.Text)
	}
	if strings.Contains(a.Text, "待配置") {
		t.Errorf("不该在结果里写占位提示：\n%s", a.Text)
	}
}

func TestMissWithoutLLMKey(t *testing.T) {
	a := Query(newFake(), "zzzqqq")
	if a.Kind != "miss" || !a.NeedsLLM {
		t.Fatalf("查不到的词应当 miss + NeedsLLM，得到 Kind=%q NeedsLLM=%v", a.Kind, a.NeedsLLM)
	}
}

func TestExchangeLabels(t *testing.T) {
	// README 那张表：0 是原形，p 是过去式
	got := exchangeText("p:ran/i:running/0:run")
	for _, want := range []string{"过去式 ran", "现在分词 running", "原形 run"} {
		if !strings.Contains(got, want) {
			t.Errorf("词形标签里缺 %q：%s", want, got)
		}
	}
}

func TestInflectionCandidates(t *testing.T) {
	cases := map[string]string{
		"runned":   "run",
		"stopping": "stop",
		"applies":  "apply",
		"happier":  "",
		"studied":  "study",
		"boxes":    "box",
	}
	for in, want := range cases {
		got := strings.Join(inflectionCandidates(in), ",")
		if want != "" && !strings.Contains(got, want) {
			t.Errorf("%s 的候选里应有 %s，实际 %s", in, want, got)
		}
	}
}
