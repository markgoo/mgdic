// Package lexicon 管的是"词典数据本身"：把 ECDICT 的 CSV 读进来、算好归一化键与常用度分数、
// 从中文释义里抽出候选中文词串。查询与显示在 internal/query，SQLite 存取在 internal/store。
//
// 两个关键事实决定了这里的形状：
//   - ECDICT 的 CSV 里，多行内容用字面量 "\n"（反斜杠加 n）分隔，不是真正的换行；
//     读进来之后要还原成换行，界面上才能分行显示。
//   - 中文方向不能靠"整串精确匹配"：中文释义是多义、带词性的，所以要先把中文词串抽出来
//     （ZhTokens），存进 SQLite 的 zh2en 表，查询时对中文输入做贪心切词，逐段去查那张表。
package lexicon

import (
	"encoding/csv"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Entry 是一条词条，字段含义与 ECDICT CSV 的列一一对应（也照原样存进 SQLite）。
type Entry struct {
	Word        string // 英文词头，本身可能就是个词组，如 "a lot"
	Phonetic    string // 音标，可能为空
	Definition  string // 英文释义，可能为空
	Translation string // 中文释义，多行
	POS         string // 词性提示，可能为空
	Freq        int    // 常用度分数，越大越常用，只用于多候选排序
	Tag         string // 考纲标签，如 "zk cet4 cet6 ky gre"
	Oxford      bool   // 是否牛津 3000 核心词
	Collins     int    // 柯林斯星级 1~6，可能为 0
	Exchange    string // 词形变化，形如 "p:ran/i:running"，项目之间用斜杠分隔
}

// Index 是内存里的一份词典，按归一化词头排好序，供二分精确查找。
// 现在它只用来"读 CSV 并算好派生字段"（import 的输入），查询走 SQLite。
type Index struct {
	Entries []Entry
	keys    []string // 与 Entries 同序的归一化键
}

// ReadCSV 只把 CSV 读成词条，不建任何索引：import 一次约几秒，之后启动就不用再读了。
// 66MB 的文件，实测读+算派生字段约 4 秒。
func ReadCSV(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, want := range []string{"word", "translation"} {
		if _, ok := col[want]; !ok {
			return nil, columnError("CSV 缺少必需的列: " + want)
		}
	}
	at := func(name string, rec []string) string {
		i := col[name]
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	out := make([]Entry, 0, 1<<17)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		word := at("word", rec)
		if word == "" {
			continue
		}
		ox := at("oxford", rec)
		out = append(out, Entry{
			Word:        word,
			Phonetic:    at("phonetic", rec),
			Definition:  unescape(at("definition", rec)),
			Translation: unescape(at("translation", rec)),
			POS:         at("pos", rec),
			Freq:        scoreOf(at("collins", rec), at("bnc", rec), at("frq", rec), ox),
			Tag:         at("tag", rec),
			Oxford:      ox == "1" || ox == "true",
			Collins:     atoi(at("collins", rec)),
			Exchange:    at("exchange", rec),
		})
	}
	return out, nil
}

// NewIndex 把词条按归一化词头排好序（只干这一件事）。
func NewIndex(entries []Entry) *Index {
	ix := &Index{Entries: entries, keys: make([]string, len(entries))}
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return NormKey(entries[order[a]].Word) < NormKey(entries[order[b]].Word)
	})
	sorted := make([]Entry, len(entries))
	for i, src := range order {
		sorted[i] = entries[src]
		ix.keys[i] = NormKey(entries[src].Word)
	}
	ix.Entries = sorted
	return ix
}

// Load 是"读 CSV + 排序"，留给命令行里想看一眼原始数据的人用。
func Load(path string) (*Index, error) {
	entries, err := ReadCSV(path)
	if err != nil {
		return nil, err
	}
	return NewIndex(entries), nil
}

// Key 是第 i 条词条的归一化词头（小写、内部空格归一）。
func (ix *Index) Key(i int) string { return ix.keys[i] }

type columnError string

func (e columnError) Error() string { return string(e) }

// NormKey 是精确匹配用的归一化词头：小写、内部空格归一。ECDICT 自己也是大小写不敏感的。
func NormKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// scoreOf 造一个"越常用越大"的分数。
//
// **ECDICT 的 bnc / frq 是词频排名，不是频率**：数字越小越常用
// （README 里"quay 在 BNC 排第 8906 名"就是这个意思）。
// 所以必须反过来算：rank 1 → 分最高。之前当成"越大越常用"用，
// 结果生僻词排在常用词前面——查"随便"给出 dishabille 就是这么来的。
func scoreOf(collins, bnc, frq, oxford string) int {
	best := 0 // 0 表示没有频率信息
	take := func(rank int) {
		if rank <= 0 {
			return
		}
		if s := 1000000 - rank; s > best {
			best = s
		}
	}
	take(atoi(frq))
	take(atoi(bnc))
	// 柯林斯星级 1~6，换算到同一量级
	if c := atoi(collins); c > 0 {
		if s := c * 150000; s > best {
			best = s
		}
	}
	// 牛津 3000 核心词兜底：即使没有排名也当常用词
	if (oxford == "1" || oxford == "true") && best < 950000 {
		best = 950000
	}
	return best
}

func atoi(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return v
		}
		v = v*10 + int(s[i]-'0')
		if v >= 1<<28 {
			return v
		}
	}
	return v
}

// unescape 把 ECDICT 里字面的 "\n"、"\r\n" 还原成真正的换行。
func unescape(s string) string {
	s = strings.ReplaceAll(s, "\\r\\n", "\n")
	s = strings.ReplaceAll(s, "\\n", "\n")
	return strings.TrimSpace(s)
}

// zhSeparators 是中文释义里能切开候选词串的标点。
const zhSeparators = "\n；;，,、。：:()（）[]【】=＝/| \t～~“”\"'.+-*#"

// ZhTokens 从中文释义里抽出候选中文词串：切分隔符、去词性前缀、只留含汉字且长度 1~8 的段。
// 抽出来的东西原样进 SQLite 的 zh2en 表，中文方向的查询全靠它。
func ZhTokens(s string) []string {
	arr := strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(zhSeparators, r)
	})
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		a = stripPOS(a)
		r := []rune(a)
		if len(r) > 8 {
			r = r[:8]
		}
		if len(r) < 1 || !hasHan(r) {
			continue
		}
		out = append(out, string(r))
	}
	return out
}

var posPrefixes = map[string]bool{
	"n": true, "v": true, "adj": true, "adv": true, "vt": true, "vi": true,
	"prep": true, "conj": true, "aux": true, "int": true, "interj": true,
	"abbr": true, "suf": true, "pref": true, "phr": true, "art": true,
	"num": true, "pron": true, "a": true, "ad": true, "u": true, "pl": true,
}

// stripPOS 去掉 "n." / "[网络]" 这类词性与来源前缀。
func stripPOS(s string) string {
	s = strings.TrimSpace(s)
	for {
		before := s
		if i := strings.IndexByte(s, '.'); i >= 0 && i <= 4 && posPrefixes[strings.ToLower(s[:i])] {
			s = strings.TrimSpace(s[i+1:])
		}
		if strings.HasPrefix(s, "[") {
			if j := strings.IndexByte(s, ']'); j >= 0 && j <= 9 {
				s = strings.TrimSpace(s[j+1:])
			}
		}
		if s == before {
			return s
		}
	}
}

func IsHanRune(c rune) bool {
	return c >= 0x3400 && c <= 0x9fff || c >= 0xf900 && c <= 0xf9ff
}

func hasHan(r []rune) bool {
	for _, c := range r {
		if IsHanRune(c) {
			return true
		}
	}
	return false
}

// IsHanText 判断输入是不是以汉字为主，用来决定走中文方向还是英文方向。
func IsHanText(s string) bool {
	han, total := 0, 0
	for _, c := range s {
		if unicode.IsSpace(c) {
			continue
		}
		total++
		if IsHanRune(c) {
			han++
		}
	}
	return total > 0 && han*2 >= total
}

// RuneLen 是字符串的字符数（不是字节数），句子长度判断用它。
func RuneLen(s string) int { return utf8.RuneCountInString(s) }
