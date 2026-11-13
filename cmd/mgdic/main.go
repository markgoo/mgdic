// mgdic 是一个只用一个功能的本地词典：Alt+E 呼出小窗，输入词/词组/句子，看结果。
//
// 用法：
//
//	mgdic                    启动窗口（默认）
//	mgdic import [--force]   从 CSV 建 SQLite 词典库（一次即可，CSV 变了会提示重建）
//	mgdic q apple pear       命令行查询，方便验证词典质量
//	mgdic config             看配置文件在哪、库里有什么
//	mgdic bench              量一下启动耗时
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mgdic/internal/config"
	"mgdic/internal/lexicon"
	"mgdic/internal/llm"
	"mgdic/internal/query"
	"mgdic/internal/store"
	"mgdic/internal/version"
	"mgdic/internal/winui"
)

type cli struct {
	csv       string
	db        string
	noLLM     bool
	force     bool
	quitAfter time.Duration
	selfcheck bool
	debug     bool
}

func main() {
	args := os.Args[1:]
	cmd := "ui"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "q", "query", "ui", "config", "bench", "import":
			cmd = args[0]
			args = args[1:]
		default:
			cmd = "q" // 允许 `mgdic apple` 这种直接查词的写法
		}
	}

	// GUI 版没有控制台。跑命令行子命令、或带了任何参数（--selfcheck 等）时，
	// 挂到父控制台上，输出才看得见。双击启动时没有父控制台，这一步什么也不做。
	if cmd != "ui" || len(os.Args) > 1 {
		attachParentConsole()
	}

	c := cli{}
	fs := flag.NewFlagSet("mgdic "+cmd, flag.ExitOnError)
	fs.StringVar(&c.csv, "csv", "", "ECDICT CSV 路径（默认用配置文件里写的）")
	fs.StringVar(&c.db, "db", "", "词典库路径（默认用配置文件里写的）")
	fs.BoolVar(&c.force, "force", false, "import 时无条件重建")
	fs.BoolVar(&c.noLLM, "no-llm", false, "完全不联网：句子也只给逐词结果")
	fs.DurationVar(&c.quitAfter, "quit-after", 0, "呼出后这么久自动退出（0 表示不退出），冒烟测试用")
	fs.BoolVar(&c.selfcheck, "selfcheck", false, "把每步 Win32 调用的成败打印出来再退出")
	fs.BoolVar(&c.debug, "debug", false, "打印耗时等诊断信息")
	_ = fs.Parse(args)

	cfg, cfgPath, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置读取出错:", err)
		os.Exit(1)
	}
	if c.csv != "" {
		cfg.DictCSV = c.csv
	}
	if c.db != "" {
		cfg.DictDB = c.db
	}

	if cmd == "import" {
		os.Exit(runImport(cfg, c))
	}

	if cmd == "config" {
		printConfig(cfg, cfgPath)
		return
	}

	// 剩下三个命令都要词典库
	t0 := time.Now()
	db, err := openDB(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer db.Close()
	if c.debug {
		fmt.Fprintf(os.Stderr, "打开词典库 %.0f ms / %s\n", float64(time.Since(t0).Microseconds())/1000, db.Path())
	}

	switch cmd {
	case "q", "query":
		os.Exit(runQuery(cfg, db, strings.Join(args, " "), c))
	case "bench":
		entries, zhTokens, err := db.Stats()
		if err != nil {
			fmt.Fprintln(os.Stderr, "读库失败:", err)
			os.Exit(2)
		}
		fmt.Printf("打开词典库到可查询: %.0f ms（%d 条词条 / %d 个中文索引）\n",
			float64(time.Since(t0).Microseconds())/1000, entries, zhTokens)
		return
	default:
		os.Exit(runUI(cfg, cfgPath, db, c))
	}
}

func openDB(cfg *config.Config) (*store.DB, error) {
	if _, err := os.Stat(cfg.DictDB); err != nil {
		// 首次运行：仓库里不带 168MB 的库，只带源码。
		// 用户把 ecdict.csv 放到程序旁边，这里就自动建库——省得还要记一条命令。
		if _, csvErr := os.Stat(cfg.DictCSV); csvErr == nil {
			fmt.Printf("首次运行：正在建立词典库（约 10 秒，只需一次）…\n  源 CSV: %s\n  目标库: %s\n",
				cfg.DictCSV, cfg.DictDB)
			st, impErr := store.Import(cfg.DictCSV, cfg.DictDB, func(f string, a ...any) {
				fmt.Printf(f+"\n", a...)
			})
			if impErr != nil {
				return nil, fmt.Errorf("自动建库失败: %w", impErr)
			}
			fmt.Printf("词典库建好了：%d 条词条 / %.0f MB\n",
				st.Rows, float64(st.SizeBytes)/(1<<20))
		} else {
			return nil, fmt.Errorf("还没有词典库（%s），也没找到源 CSV。\n\n"+
				"两种办法：\n"+
				"  1) 下载 ecdict.csv 放到本程序旁边，然后重新启动（会自动建库）\n"+
				"     curl.exe -sSL -o ecdict.csv https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv\n"+
				"  2) 从别处拷一份 mgdic.db 放到本程序旁边\n\n"+
				"源 CSV 应该在：%s", cfg.DictDB, cfg.DictCSV)
		}
	}
	db, err := store.Open(cfg.DictDB)
	if err != nil {
		return nil, err
	}
	// CSV 变了才提示重建，而且只是提示，不会自己动手
	if stale, why := store.NeedsRebuild(cfg.DictCSV, cfg.DictDB); stale {
		fmt.Fprintf(os.Stderr, "提示：%s；需要重建时手动运行 mgdic import\n", why)
	}
	return db, nil
}

func runImport(cfg *config.Config, c cli) int {
	if _, err := os.Stat(cfg.DictCSV); err != nil {
		fmt.Fprintf(os.Stderr, "找不到源 CSV: %s\n先下载：curl.exe -sSL -o .vendor\\ecdict.csv https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv\n", cfg.DictCSV)
		return 2
	}
	if !c.force {
		if stale, why := store.NeedsRebuild(cfg.DictCSV, cfg.DictDB); !stale {
			fmt.Printf("词典库已是最新（%s），跳过。要强制重建加 --force\n", cfg.DictDB)
			return 0
		} else if why != "" {
			fmt.Printf("需要重建：%s\n", why)
		}
	}
	fmt.Printf("源 CSV : %s\n目标库 : %s\n", cfg.DictCSV, cfg.DictDB)
	st, err := store.Import(cfg.DictCSV, cfg.DictDB, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "建库失败:", err)
		return 3
	}
	fmt.Printf("完成：%d 条词条 / %d 条中文索引 / 库文件 %.1f MB / 用时 %.1f s\n",
		st.Rows, st.ZhRows, float64(st.SizeBytes)/(1<<20), st.Elapsed.Seconds())
	return 0
}

func runQuery(cfg *config.Config, db *store.DB, text string, c cli) int {
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "要查的词没有给：mgdic q apple")
		return 3
	}
	cl := newLLM(cfg, c.noLLM)
	a := query.Query(db, text)
	fmt.Println(a.Text)
	if a.NeedsLLM && cl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TimeoutDur.Duration)
		defer cancel()
		to := "中文"
		if lexicon.IsHanText(text) {
			to = "英文"
		}
		got, err := cl.Translate(ctx, text, to)
		if err != nil {
			fmt.Println("  [DeepSeek] 翻译失败:", err)
		} else {
			fmt.Println("  [DeepSeek]", got)
		}
	} else if a.NeedsLLM {
		fmt.Println("  （整句翻译要联网：在配置里填 api_key 后重跑）")
	}
	return 0
}

func runUI(cfg *config.Config, cfgPath string, db *store.DB, c cli) int {
	cl := newLLM(cfg, c.noLLM)

	lookup := func(text string) winui.Result {
		a := query.Query(db, text)
		res := winui.Result{NeedsLLM: a.NeedsLLM && cl != nil, Note: a.Note}
		res.Chips = a.Chips
		for _, c := range a.Cards {
			res.Cards = append(res.Cards, winui.Card{
				Title: c.Title, Lines: c.Lines, Meta: c.Meta, Copy: c.Copy,
			})
		}
		if a.NeedsLLM && cl == nil {
			// 没有 key 时要说清楚为什么按回车没反应
			res.Note = "整句翻译需要 api_key：config.json 里的 api_key"
			res.NeedsLLM = false
		}
		return res
	}
	translate := func(text string) <-chan string {
		ch := make(chan string, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), cfg.TimeoutDur.Duration)
			defer cancel()
			to := "中文"
			if lexicon.IsHanText(text) {
				to = "英文"
			}
			got, err := cl.Translate(ctx, text, to)
			if err != nil {
				got = "翻译失败: " + err.Error()
			}
			ch <- got
		}()
		return ch
	}
	// 日志可以关：config.json 里 "log": false 就完全不写文件
	logPath := ""
	if cfg.Log {
		logPath = filepath.Join(filepath.Dir(cfgPath), "startup.log")
	}
	if err := winui.Run(winui.Options{
		Hotkey:     cfg.Hotkey,
		Query:      lookup,
		Translate:  translate,
		ConfigPath: cfgPath,
		Version:    version.Version,
		LogPath:    logPath,
		HideAfter:  c.quitAfter,
		Selfcheck:  c.selfcheck,
		Debug:      c.debug,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "界面没起来:", err)
		// 双击启动时没有控制台可看，失败必须弹窗，否则用户只看到黑框一闪
		if !c.selfcheck {
			winui.ShowError("mgdic 启动失败",
				err.Error()+"\n\n详细信息已写入：\n"+
					filepath.Join(filepath.Dir(cfgPath), "startup.log"))
		}
		return 4
	}
	return 0
}

func newLLM(cfg *config.Config, disabled bool) *llm.Client {
	if disabled {
		return nil
	}
	return llm.New(llm.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		Timeout: cfg.TimeoutDur.Duration,
	})
}

func printConfig(cfg *config.Config, cfgPath string) {
	fmt.Println("mgdic v" + version.Version)
	fmt.Println("配置文件:", cfgPath)
	fmt.Println("  model     :", cfg.Model)
	fmt.Println("  base_url  :", cfg.BaseURL)
	fmt.Println("  hotkey    :", cfg.Hotkey)
	fmt.Println("  api_key   :", maskKey(cfg.APIKey))
	fmt.Println("  dict_csv  :", cfg.DictCSV)
	fmt.Println("  dict_db   :", cfg.DictDB)
	if cfg.Log {
		fmt.Println("  log       : 开（" + filepath.Join(filepath.Dir(cfgPath), "startup.log") + "）")
	} else {
		fmt.Println("  log       : 关（config.json 里 \"log\": false）")
	}

	db, err := store.Open(cfg.DictDB)
	if err != nil {
		fmt.Println("  ! 词典库不可用:", err)
		return
	}
	defer db.Close()
	entries, zhTokens, err := db.Stats()
	if err != nil {
		fmt.Println("  ! 读库失败:", err)
		return
	}
	if fi, err := os.Stat(cfg.DictDB); err == nil {
		fmt.Printf("  ✓ 词典库 %d 条词条 / %d 个中文索引 / %.1f MB\n",
			entries, zhTokens, float64(fi.Size())/(1<<20))
	}
	// 只有源 CSV 存在时才谈"要不要重建"；没有 CSV 也不影响查词
	if _, err := os.Stat(cfg.DictCSV); err == nil {
		if stale, why := store.NeedsRebuild(cfg.DictCSV, cfg.DictDB); stale {
			fmt.Printf("  · 源 CSV 有变化：%s（想重建再跑 mgdic import）\n", why)
		}
	}
}

func maskKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return "（未配置，句子翻译走本地逐词）"
	}
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "…" + k[len(k)-4:]
}
