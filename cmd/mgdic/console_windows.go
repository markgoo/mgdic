package main

import (
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// GUI 版（-H windowsgui）启动时没有控制台，fmt 的输出会直接丢掉。
//
// 跑 q / import / config 这些子命令时，挂到父进程的控制台上，输出就又能看见了：
// 从 cmd 里 `mgdic.exe q apple` 和用 CLI 版行为一致；双击启动时没有父控制台，这一步什么也不做。
const attachParentProcess = ^uint32(0) // ATTACH_PARENT_PROCESS = (DWORD)-1

func attachParentConsole() {
	if runtime.GOOS != "windows" {
		return
	}
	k32 := windows.NewLazyDLL("kernel32.dll")
	if r, _, _ := k32.NewProc("GetConsoleWindow").Call(); r != 0 {
		return // 本来就有控制台（CLI 版，或从终端启动）
	}
	if r, _, _ := k32.NewProc("AttachConsole").Call(uintptr(attachParentProcess)); r == 0 {
		return // 没有父控制台
	}
	// 挂上之后 os.Stdout 还指着无效句柄，重开一个
	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	os.Stdout = out
	os.Stderr = out
}
