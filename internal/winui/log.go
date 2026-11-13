package winui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 启动日志：记录每一步 Win32 调用的成败。
//
// 为什么必须有它：双击启动时控制台会随进程消失，一旦在注册窗口类这一步失败，
// 用户看到的就是"闪退"，没有任何线索。日志把现场留下来，下次就不用靠猜。

var (
	logFile *os.File
	logPath string
)

// startLog 打开日志文件。用追加而不是截断：
// 之前用 os.Create 每次清空，结果我的排查运行把用户的失败现场盖掉了。
// 超过 256KB 才重置一次，免得越积越大。
func startLog(path string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > 256<<10 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return
	}
	logFile, logPath = f, path
	logLine("──────── 启动 " + time.Now().Format("2006-01-02 15:04:05") + " ────────")
	logLine("exe=" + selfPath())
	logLine(fmt.Sprintf("pid=%d", os.Getpid()))
}

func closeLog() {
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// LogPath 返回本次启动的日志文件路径（没开日志时为空）。
func LogPath() string { return logPath }

// logLine 写一行日志；同时打到控制台（控制台被藏起来时这行自然看不到，无害）。
func logLine(s string) {
	line := time.Now().Format("15:04:05.000") + "  " + s
	if logFile != nil {
		fmt.Fprintln(logFile, line)
		logFile.Sync()
	}
	fmt.Println("  " + s)
}

func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "?"
	}
	return p
}
