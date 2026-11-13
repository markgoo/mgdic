// Package config 把配置放在 %APPDATA%\mgdic\config.json，改完重启即可，不影响词典数据。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	DefaultBaseURL = "https://api.deepseek.com/v1"
	DefaultModel   = "deepseek-chat"
	DefaultHotkey  = "alt+e"
	DefaultTimeout = 8000 * time.Millisecond
)

type Config struct {
	APIKey     string `json:"api_key"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Hotkey     string `json:"hotkey"`
	DictCSV    string `json:"dict_csv"` // ECDICT CSV：建库的源，只在 import 时读
	DictDB     string `json:"dict_db"`  // SQLite 词典库：启动时只打开它
	Log        bool   `json:"log"`      // 是否写启动日志，默认开；设 false 就完全不写文件
	TimeoutDur Dur    `json:"-"`
	TimeoutMs  int    `json:"timeout_ms"`
}

// Dur 是给 JSON 用的时间间隔包装，避免配置文件里写 "8s" 这种字符串还得手写解析。
type Dur struct{ time.Duration }

func (d *Dur) UnmarshalJSON(b []byte) error {
	var ms int
	if err := json.Unmarshal(b, &ms); err != nil {
		return err
	}
	d.Duration = time.Duration(ms) * time.Millisecond
	return nil
}

// configPath 决定配置文件放哪。
//
// 默认就是 **exe 所在目录**：整个文件夹拷走就能用（绿色版），不往 %APPDATA% 里塞东西。
// 只有当 exe 目录写不进去时（例如装在 Program Files），才退回 %APPDATA%\mgdic。
func configPath() (string, error) {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if dirWritable(dir) {
			return filepath.Join(dir, "config.json"), nil
		}
	}
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// dirWritable 试写一个临时文件来判断目录能不能写。
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".mgdic-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// Dir 返回配置目录 %APPDATA%\mgdic，必要时创建它。
func Dir() (string, error) {
	base := os.Getenv("APPDATA")
	if runtime.GOOS != "windows" {
		base = os.Getenv("HOME")
	}
	if base == "" {
		base = "."
	}
	dir := filepath.Join(base, "mgdic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Load 读取配置；文件不存在时创建一份默认配置，并把 DEEPSEEK_API_KEY 环境变量当作兜底。
func Load() (*Config, string, error) {
	path, err := configPath()
	if err != nil {
		return nil, "", err
	}

	c := Default()
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := c.Save(path); err != nil {
			return nil, path, err
		}
	case err != nil:
		return nil, path, err
	case len(b) > 0:
		if err := json.Unmarshal(b, c); err != nil {
			return nil, path, err
		}
	}

	if c.APIKey == "" {
		if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
			c.APIKey = k
		}
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Hotkey == "" {
		c.Hotkey = DefaultHotkey
	}
	if c.TimeoutDur.Duration == 0 {
		c.TimeoutDur.Duration = DefaultTimeout
	}
	if c.TimeoutMs == 0 {
		c.TimeoutMs = int(DefaultTimeout / time.Millisecond)
	}
	c.DictCSV = resolveCSV(path, c.DictCSV)
	c.DictDB = resolveDB(path, c.DictDB)
	return c, path, nil
}

// resolveDB 决定词典库放哪：默认就是 **exe 同级的 mgdic.db**，跟配置一样为了绿色版。
// 这样"exe + mgdic.db + config.json"三件套放一个文件夹，拷到别的电脑直接能用。
func resolveDB(confPath, want string) string {
	if want != "" {
		return want
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "mgdic.db")
	}
	return filepath.Join(filepath.Dir(confPath), "mgdic.db")
}

// resolveCSV 依次试：配置里写的路径、exe 同级（含 .vendor 子目录）、exe 上一级、配置目录。
// 绿色版希望 ecdict.csv 直接放在 exe 旁边也认。
func resolveCSV(confPath, want string) string {
	if want != "" {
		if st, err := os.Stat(want); err == nil && !st.IsDir() {
			return want
		}
	}
	cands := make([]string, 0, 8)
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		cands = append(cands,
			filepath.Join(base, "ecdict.csv"),
			filepath.Join(base, ".vendor", "ecdict.csv"),
			filepath.Join(base, "..", ".vendor", "ecdict.csv"),
			filepath.Join(base, "..", "ecdict.csv"),
		)
	}
	confDir := filepath.Dir(confPath)
	cands = append(cands,
		filepath.Join(confDir, "ecdict.csv"),
		filepath.Join(confDir, ".vendor", "ecdict.csv"),
	)
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if want != "" {
		return want
	}
	if len(cands) > 0 {
		return cands[0]
	}
	return "ecdict.csv"
}

// Default 是一份能直接用的配置：只有句子翻译需要联网，词典查询完全不依赖 key。
//
// 注意 Log 默认 true：我们从 Default() 出发再 Unmarshal，
// 老配置里没有 "log" 这个键时，这个值不会被覆盖，仍然是开。
func Default() *Config {
	return &Config{
		BaseURL:    DefaultBaseURL,
		Model:      DefaultModel,
		Hotkey:     DefaultHotkey,
		Log:        true,
		TimeoutMs:  int(DefaultTimeout / time.Millisecond),
		TimeoutDur: Dur{Duration: DefaultTimeout},
	}
}

func (c *Config) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
