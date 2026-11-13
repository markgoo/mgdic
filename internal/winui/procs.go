package winui

import (
	"fmt"
	"sort"

	"golang.org/x/sys/windows"
)

// checkProcs 在启动时把要用到的 Win32 导出全部解析一遍。
//
// 存在的理由：x/sys 的 LazyProc 在导出名不存在时是 **panic**，不是返回错误。
// 之前把 DispatchMessageW 写成了 DispatchMessage，结果程序一路跑到消息循环才崩，
// 用户看到的就是"闪退"。现在这种情况会变成一句能读懂的错误。
func checkProcs() error {
	all := map[string]*windows.LazyProc{
		// user32
		"RegisterClassW":           procRegisterClass,
		"CreateWindowExW":          procCreateWindow,
		"ShowWindow":               procShow,
		"DestroyWindow":            procDestroy,
		"SetWindowPos":             procSetPos,
		"GetSystemMetrics":         procGetMetrics,
		"GetCursorPos":             procGetCursor,
		"SetForegroundWindow":      procForeground,
		"GetForegroundWindow":      procGetForeground,
		"GetWindowThreadProcessId": procGetWndThread,
		"AttachThreadInput":        procAttachInput,
		"SetFocus":                 procSetFocus,
		"SetWindowTextW":           procSetText,
		"GetWindowTextW":           procGetText,
		"GetWindowTextLengthW":     procGetTextLen,
		"SendMessageW":             procSend,
		"PostMessageW":             procPost,
		"RegisterHotKey":           procRegHot,
		"UnregisterHotKey":         procUnregHot,
		"SetTimer":                 procSetTimer,
		"KillTimer":                procKillTimer,
		"GetAsyncKeyState":         procAsyncKey,
		"DefWindowProcW":           procDefProc,
		"GetMessageW":              procGetMsg,
		"TranslateMessage":         procTranslate,
		"DispatchMessageW":         procDispatch,
		"PostQuitMessage":          procQuit,
		"GetDpiForWindow":          procDpi,
		"GetWindowRect":            procGetRect,
		"GetThreadDesktop":         procGetDesk,
		"OpenDesktopW":             procOpenDesk,
		"SetThreadDesktop":         procSetDesk,
		"SetProcessDPIAware":       procDpiAware,
		"MonitorFromPoint":         procMonitorFromPoint,
		"GetMonitorInfoW":          procGetMonitorInfo,
		"IsWindowVisible":          procIsWindowVisible,
		"SetWindowRgn":             procSetWindowRgn,
		"BeginPaint":               procBeginPaint,
		"EndPaint":                 procEndPaint,
		"GetClientRect":            procGetClientRect,
		"InvalidateRect":           procInvalidateRect,
		"FillRect":                 procFillRect,
		"CreateRoundRectRgn":       procCreateRoundRgn,
		"RoundRect":                procRoundRect,
		"CreateSolidBrush":         procCreateSolidBrush,
		"CreatePen":                procCreatePen,
		"SelectObject":             procSelectObject,
		"GetStockObject":           procGetStockObject,
		"SetTextColor":             procSetTextColor,
		"SetBkColor":               procSetBkColor,
		"DwmSetWindowAttribute":    procDwmSetAttribute,
		"MessageBoxW":              procMessageBox,
		"RegisterWindowMessageW":   procRegisterMsg,
		"LoadIconW":                procLoadIcon,
		"CreatePopupMenu":          procCreatePopup,
		"AppendMenuW":              procAppendMenu,
		"TrackPopupMenu":           procTrackPopup,
		"DestroyMenu":              procDestroyMenu,
		"CreateIconIndirect":       procCreateIconInd,
		// gdi32
		"CreateFontIndirectW": procCreateFont,
		"CreateDIBSection":    procCreateDIB,
		"CreateBitmap":        procCreateBitmap,
		"DeleteObject":        procDeleteObject,
		"CreateCompatibleDC":  procCreateDC,
		"DeleteDC":            procDeleteDC,
		// kernel32
		"GetCurrentThreadId":    procThreadID,
		"GetConsoleWindow":      procGetConsole,
		"GetConsoleProcessList": procConsoleProcs,
		// shell32
		"Shell_NotifyIconW": procNotifyIcon,
		"ShellExecuteW":     procShellExecute,
	}

	var missing []string
	for name, p := range all {
		if err := p.Find(); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("这些 Win32 导出找不到（名字写错了？）：%v", missing)
}
