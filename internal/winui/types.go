package winui

import (
	"runtime"
	"strings"
	"unsafe"
)

// Card 是一个词条的结果。
//
// 界面上分两层显示：Lines 放进圆角框（点一下整块复制），
// Title / Meta 放进下方只读文本框（可以拖选其中一段复制）。
type Card struct {
	Title string   // 词头 + 音标
	Lines []string // 主翻译：圆角框里的内容
	Meta  string   // 音标/释义/考纲等辅助信息，文本框里
	Copy  string   // 点圆角框时复制的内容
}

// Result 是一次查询的结果。
type Result struct {
	Cards    []Card
	Chips    []string // 句子查询时切出来的词（文本框里一行显示）
	Note     string   // 提示或译文
	NeedsLLM bool
}

// hitTarget 是一块可点击的区域和它对应的文字。
type hitTarget struct {
	r    rect
	text string
}

// ---- 剪贴板 ----

const gmemMoveable = 0x0002

// setClipboardText 把一段文字放进剪贴板。
//
// OpenClipboard 经常因为"别的程序正开着剪贴板"而返回 Access is denied，
// 所以用 NULL（关联当前任务而不是某个窗口）并重试几次。
func setClipboardText(owner handle, s string) bool {
	u := utf16(s)
	if len(u) == 0 {
		return false
	}
	opened := false
	for i := 0; i < 10; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		runtime.Gosched()
	}
	if !opened {
		if r, _, _ := procOpenClipboard.Call(owner); r == 0 {
			return false
		}
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	size := uintptr(len(u) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return false
	}
	// 用 RtlMoveMemory 按地址拷，避免把 uintptr 转回 Go 指针（那种转换是悬空的，vet 也会拦）
	procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&u[0])), size)
	runtime.KeepAlive(&u)
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return false
	}
	return true
}

// copyText 把一段文字放进剪贴板。
func copyText(s, what string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if setClipboardText(app.hwnd, s) {
		logLine("已复制：" + what)
	} else {
		logLine("复制失败：" + what)
	}
}
