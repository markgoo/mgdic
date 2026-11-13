// Package store 把词典装进一个 SQLite 文件，并负责查询。
//
// 为什么值得多一个文件：启动时不用再从 66MB 的 CSV 现建索引（实测约 10 秒），
// 打开一个库就是全部准备工作；中文方向的倒排表也因此可以持久化，不必每次重算。
//
// 库的形状：
//
//	entries(id, word, key_norm, phonetic, definition, translation, pos, tag, exchange, freq, oxford, collins)
//	zh2en(tok, entry_id, freq)      中文词串 -> 命中它的英文词条
//	meta(key, value)                schema 版本与源 CSV 指纹，用来判断要不要重建
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 实现：不带 DLL、不带 CGO，运行时对用户完全隐形

	"mgdic/internal/lexicon"
)

// SchemaVersion 变了就必须重建库；用 build 标记是让人一眼看出"这库是给哪个版本写的"。
const SchemaVersion = "1"

// DB 是一个已经打开的词典库。
type DB struct {
	db   *sql.DB
	path string
}

// entryCols 是所有查询共用的列顺序，scanEntry 按同一顺序读回来。
const entryCols = `e.word, e.phonetic, e.definition, e.translation, e.pos, e.tag, e.exchange, e.freq, e.oxford, e.collins`

// Open 以只读方式打开词典库。查询阶段不需要写权限，也不该有。
func Open(path string) (*DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1) // 单连接：这个程序的查询全是短事务，连接池只会添乱
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("打不开词典库 %s: %w", path, err)
	}
	st := &DB{db: d, path: path}
	if err := st.checkSchema(); err != nil {
		d.Close()
		return nil, err
	}
	return st, nil
}

func (s *DB) checkSchema() error {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v)
	if err != nil {
		return fmt.Errorf("词典库缺少版本信息（可能不是 mgdic 建的）：%w", err)
	}
	if v != SchemaVersion {
		return fmt.Errorf("词典库版本是 %s，程序要 %s；跑一次 mgdic import 重建", v, SchemaVersion)
	}
	return nil
}

func (s *DB) Close() error { return s.db.Close() }

// Path 返回库文件路径，命令行里报错时用得上。
func (s *DB) Path() string { return s.path }

// Stats 是库里的基本数字，config 命令会打印出来。
func (s *DB) Stats() (entries, zhTokens int64, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&entries); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT COUNT(DISTINCT tok) FROM zh2en`).Scan(&zhTokens)
	return
}

// Entry 是一次查询命中的词条。
type Entry = lexicon.Entry

// LookupEN 按归一化词头精确查找，多条同形词条按常用度降序。
func (s *DB) LookupEN(key string, limit int) ([]Entry, error) {
	q := `SELECT ` + entryCols + ` FROM entries e WHERE e.key_norm = ? ORDER BY e.freq DESC LIMIT ?`
	return s.queryEntries(q, key, limit)
}

// PrefixEN 取以 key 开头的候选，按常用度降序，常用度相同则短词优先。
// 短词优先是有原因的：ECDICT 里"applicaton fee"这类长词组本身就是错拼词条，
// 排在前面只会把真正想要的词挤掉。
func (s *DB) PrefixEN(key string, limit int) ([]Entry, error) {
	upper := key + "\U0010FFFF" // 比任何以 key 开头的字符串都大
	q := `SELECT ` + entryCols + ` FROM entries e
	      WHERE e.key_norm >= ? AND e.key_norm < ?
	      ORDER BY e.freq DESC, length(e.word) ASC LIMIT ?`
	return s.queryEntries(q, key, upper, limit)
}

// LookupMany 一次查一批归一化词头，用于"改一个字母再查"的纠错：
// 把几百个候选键放进一条 IN 查询，比逐个发查询少几百次往返。
func (s *DB) LookupMany(keys []string, limit int) ([]Entry, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	const chunk = 400 // 远低于 SQLite 的变量上限，留足余量
	var out []Entry
	for start := 0; start < len(keys); start += chunk {
		end := start + chunk
		if end > len(keys) {
			end = len(keys)
		}
		part := keys[start:end]
		holders := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		q := `SELECT ` + entryCols + ` FROM entries e
		      WHERE e.key_norm IN (` + holders + `) ORDER BY e.freq DESC LIMIT ?`
		args := make([]any, 0, len(part)+1)
		for _, k := range part {
			args = append(args, k)
		}
		args = append(args, limit)
		got, err := s.queryEntries(q, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (s *DB) queryEntries(q string, args ...any) ([]Entry, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Word, &e.Phonetic, &e.Definition, &e.Translation,
			&e.POS, &e.Tag, &e.Exchange, &e.Freq, &e.Oxford, &e.Collins); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ZhCandidates 一次问完一批中文词串，每个词串给前 perToken 个英文词条（按常用度降序）。
// 一次查询解决：窗口函数按词串分区取前 N，避免对每个候选串单独发一次查询。
func (s *DB) ZhCandidates(tokens []string, perToken int) (map[string][]Entry, error) {
	out := make(map[string][]Entry, len(tokens))
	if len(tokens) == 0 {
		return out, nil
	}
	holders := strings.TrimSuffix(strings.Repeat("?,", len(tokens)), ",")
	q := `SELECT tok, word, phonetic, definition, translation, pos, tag, exchange, freq, oxford, collins FROM (
	        SELECT z.tok AS tok, ` + entryCols + `,
	               ROW_NUMBER() OVER (PARTITION BY z.tok ORDER BY z.freq DESC) AS rn
	          FROM zh2en z JOIN entries e ON e.id = z.entry_id
	         WHERE z.tok IN (` + holders + `)
	      ) WHERE rn <= ?`
	args := make([]any, 0, len(tokens)+1)
	for _, t := range tokens {
		args = append(args, t)
	}
	args = append(args, perToken)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tok string
		var e Entry
		if err := rows.Scan(&tok, &e.Word, &e.Phonetic, &e.Definition, &e.Translation,
			&e.POS, &e.Tag, &e.Exchange, &e.Freq, &e.Oxford, &e.Collins); err != nil {
			return nil, err
		}
		out[tok] = append(out[tok], e)
	}
	return out, rows.Err()
}

// ---- 建库 ----

// Stats 是 import 的结果，命令行打印用。
type ImportStats struct {
	Rows      int
	ZhRows    int
	SizeBytes int64
	Elapsed   time.Duration
}

// NeedsRebuild 判断源 CSV 是否比库新：大小或修改时间变了就要重建。
// 这就是"缓存"那件事——库是 CSV 的缓存，指纹对不上就重做。
func NeedsRebuild(csvPath, dbPath string) (bool, string) {
	cst, err := os.Stat(csvPath)
	if err != nil {
		return false, "找不到源 CSV: " + err.Error()
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		return true, "库文件读不了"
	}
	defer db.Close()
	var size, mod string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='csv_size'`).Scan(&size); err != nil {
		return true, "库里没有源文件指纹"
	}
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='csv_mod'`).Scan(&mod); err != nil {
		return true, "库里没有源文件指纹"
	}
	if size != fmt.Sprint(cst.Size()) {
		return true, "源 CSV 大小变了"
	}
	if mod != fmt.Sprint(cst.ModTime().Unix()) {
		return true, "源 CSV 修改时间变了"
	}
	return false, ""
}

// Import 从 CSV 建一个新库：先写临时文件，成功后再换上去，中途失败不会留下半个库。
func Import(csvPath, dbPath string, logf func(format string, args ...any)) (ImportStats, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	start := time.Now()

	cst, err := os.Stat(csvPath)
	if err != nil {
		return ImportStats{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return ImportStats{}, err
	}
	tmp := dbPath + ".tmp"
	os.Remove(tmp)

	// 建库期间关掉日志与 fsync：这是可重来的临时文件，崩了大不了重跑
	dsn := "file:" + filepath.ToSlash(tmp) + "?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return ImportStats{}, err
	}
	d.SetMaxOpenConns(1)
	defer func() {
		d.Close()
		os.Remove(tmp) // 成功时它已经被 rename 掉了，这里是失败路径的清理
	}()

	if err := createSchema(d); err != nil {
		return ImportStats{}, err
	}

	entries, err := lexicon.ReadCSV(csvPath)
	if err != nil {
		return ImportStats{}, err
	}
	logf("读到 %d 条词条，开始写库…", len(entries))

	stats, err := fill(d, entries, logf)
	if err != nil {
		return ImportStats{}, err
	}
	if err := writeMeta(d, cst, len(entries)); err != nil {
		return ImportStats{}, err
	}
	if err := d.Close(); err != nil {
		return ImportStats{}, err
	}

	// 换上去：先删旧的再改名，Windows 上 rename 不能覆盖已存在的文件
	os.Remove(dbPath)
	if err := os.Rename(tmp, dbPath); err != nil {
		return ImportStats{}, fmt.Errorf("替换库文件失败: %w", err)
	}

	stats.Elapsed = time.Since(start)
	if fi, err := os.Stat(dbPath); err == nil {
		stats.SizeBytes = fi.Size()
	}
	return stats, nil
}

func createSchema(d *sql.DB) error {
	_, err := d.Exec(`
CREATE TABLE entries (
  id          INTEGER PRIMARY KEY,
  word        TEXT NOT NULL,
  key_norm    TEXT NOT NULL,
  phonetic    TEXT NOT NULL DEFAULT '',
  definition  TEXT NOT NULL DEFAULT '',
  translation TEXT NOT NULL DEFAULT '',
  pos         TEXT NOT NULL DEFAULT '',
  tag         TEXT NOT NULL DEFAULT '',
  exchange    TEXT NOT NULL DEFAULT '',
  freq        INTEGER NOT NULL DEFAULT 0,
  oxford      INTEGER NOT NULL DEFAULT 0,
  collins     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE zh2en (
  tok      TEXT NOT NULL,
  entry_id INTEGER NOT NULL,
  freq     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`)
	return err
}

func fill(d *sql.DB, entries []lexicon.Entry, logf func(string, ...any)) (ImportStats, error) {
	tx, err := d.Begin()
	if err != nil {
		return ImportStats{}, err
	}
	defer tx.Rollback()

	insE, err := tx.Prepare(`INSERT INTO entries
	  (id, word, key_norm, phonetic, definition, translation, pos, tag, exchange, freq, oxford, collins)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return ImportStats{}, err
	}
	defer insE.Close()
	insZ, err := tx.Prepare(`INSERT INTO zh2en (tok, entry_id, freq) VALUES (?,?,?)`)
	if err != nil {
		return ImportStats{}, err
	}
	defer insZ.Close()

	seen := make(map[string]struct{}, 8) // 每个词条内去重，避免同一个中文串挂两遍
	zhRows := 0
	for i, e := range entries {
		id := i + 1
		ox := 0
		if e.Oxford {
			ox = 1
		}
		if _, err := insE.Exec(id, e.Word, lexicon.NormKey(e.Word), e.Phonetic, e.Definition,
			e.Translation, e.POS, e.Tag, e.Exchange, e.Freq, ox, e.Collins); err != nil {
			return ImportStats{}, fmt.Errorf("第 %d 条词条写入失败: %w", id, err)
		}
		clear(seen)
		for _, tok := range lexicon.ZhTokens(e.Translation) {
			if _, dup := seen[tok]; dup {
				continue
			}
			seen[tok] = struct{}{}
			if _, err := insZ.Exec(tok, id, e.Freq); err != nil {
				return ImportStats{}, fmt.Errorf("中文索引写入失败（%s）: %w", tok, err)
			}
			zhRows++
		}
		if (i+1)%100000 == 0 {
			logf("  …已写 %d / %d 条", i+1, len(entries))
		}
	}
	if err := tx.Commit(); err != nil {
		return ImportStats{}, err
	}

	// 索引放在插入之后建：先灌数据再建索引，比边插边维护快得多
	logf("建索引…")
	if _, err := d.Exec(`CREATE INDEX idx_entries_key ON entries(key_norm, freq DESC)`); err != nil {
		return ImportStats{}, err
	}
	if _, err := d.Exec(`CREATE INDEX idx_zh2en_tok ON zh2en(tok, freq DESC)`); err != nil {
		return ImportStats{}, err
	}
	if _, err := d.Exec(`ANALYZE`); err != nil {
		return ImportStats{}, err
	}
	// 试过在建索引后补 VACUUM：实测体积从 168.1MB 到 168.1MB，一点没压下来，纯属白花 2.6 秒。
	// 体积的大头是 77 万条释义文本本身，不是空闲页，所以这里不做压实。
	return ImportStats{Rows: len(entries), ZhRows: zhRows}, nil
}

func writeMeta(d *sql.DB, cst os.FileInfo, rows int) error {
	kv := map[string]string{
		"schema":   SchemaVersion,
		"csv_size": fmt.Sprint(cst.Size()),
		"csv_mod":  fmt.Sprint(cst.ModTime().Unix()),
		"rows":     fmt.Sprint(rows),
		"built_at": time.Now().Format(time.RFC3339),
	}
	for k, v := range kv {
		if _, err := d.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	return nil
}
