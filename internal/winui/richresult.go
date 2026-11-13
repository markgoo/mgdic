package winui

import (
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

// 结果区用 RichEdit 只读文本框。
//
// 为什么不是普通 EDIT：普通 EDIT 也能选，但 RichEdit 的背景色、行距、右键菜单都更好控，
// 而且它是"只读但可选"的标准做法——用户可以拖选其中一段再 Ctrl+C，而不是只能整块复制。

const emSetBkColor = 0x0444 // EM_SETBKGNDCOLOR

var (
	msftedit        = windows.NewLazyDLL("Msftedit.dll")
	procLoadLibrary = kernel32.NewProc("LoadLibraryW")
)

// createRichResult 建那个只读结果框。
func createRichResult(parent handle, l layout) (handle, error) {
	// 必须先加载 Msftedit.dll，否则系统里没有 RICHEDIT50W 这个窗口类
	dll := utf16("Msftedit.dll")
	procLoadLibrary.Call(pptr(&dll[0]))
	runtime.KeepAlive(&dll)

	cls := utf16("RICHEDIT50W")
	title := utf16("")
	style := uintptr(wsChild | wsVisible | wsVScroll | esMultiline | esAutoVScroll | esReadOnly)
	r, e := call(procCreateWindow, 0, pptr(&cls[0]), pptr(&title[0]),
		style,
		uintptr(l.resX()), uintptr(l.resY()), uintptr(l.resW()), uintptr(l.resH()),
		parent, 0, 0, 0)
	runtime.KeepAlive(&cls)
	runtime.KeepAlive(&title)
	if r == 0 {
		return 0, e
	}
	return r, nil
}

// setResultText 把文本写进结果框。
// RichEdit 要 CRLF 才断行，只给 \n 会被当成空格。
func setResultText(text string) {
	if app.hResult == 0 {
		return
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	b := utf16(text)
	if len(b) == 0 {
		b = utf16(" ")
	}
	call(procSetText, app.hResult, pptr(&b[0]))
	runtime.KeepAlive(&b)
}

// resultToText 把结构化结果拍平成纯文本喂给结果框。
func resultToText(r Result) string {
	var b strings.Builder
	for i, c := range r.Cards {
		if i > 0 {
			b.WriteString("\n")
		}
		if c.Title != "" {
			b.WriteString(c.Title + "\n")
		}
		for _, line := range c.Lines {
			b.WriteString("  " + line + "\n")
		}
		if c.Meta != "" {
			b.WriteString("  " + c.Meta + "\n")
		}
	}
	if len(r.Chips) > 0 {
		b.WriteString(strings.Join(r.Chips, "   "))
		b.WriteString("\n")
	}
	if r.Note != "" {
		b.WriteString(r.Note)
	}
	out := strings.TrimRight(b.String(), "\n")
	if strings.TrimSpace(out) == "" {
		return placeholderText
	}
	return out
}
