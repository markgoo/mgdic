package winui

import (
	"testing"
	"unsafe"
)

// LOGFONTW 的字段顺序和大小必须和 C 完全一致。
//
// 这里曾经漏掉 lfWeight/lfItalic/lfUnderline/lfStrikeOut，结构体短了 4 字节，
// 于是字体名、字重、ClearType 平滑**全部失效**，而程序不会报任何错。
// x64 上正确值是 92 字节。
func TestLogFontLayout(t *testing.T) {
	const want = 92
	if got := unsafe.Sizeof(logFont{}); got != want {
		t.Fatalf("sizeof(LOGFONTW) = %d，Windows 要 %d", got, want)
	}
	// 关键字段的偏移也必须对：Weight 在 16，CharSet 在 23，Quality 在 26，FaceName 在 28
	var lf logFont
	base := uintptr(unsafe.Pointer(&lf))
	off := func(p unsafe.Pointer) uintptr { return uintptr(p) - base }
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"lfWeight", off(unsafe.Pointer(&lf.Weight)), 16},
		{"lfItalic", off(unsafe.Pointer(&lf.Italic)), 20},
		{"lfUnderline", off(unsafe.Pointer(&lf.Underline)), 21},
		{"lfStrikeOut", off(unsafe.Pointer(&lf.StrikeOut)), 22},
		{"lfCharSet", off(unsafe.Pointer(&lf.CharSet)), 23},
		{"lfOutPrecision", off(unsafe.Pointer(&lf.OutPrecision)), 24},
		{"lfQuality", off(unsafe.Pointer(&lf.Quality)), 26},
		{"lfFaceName", off(unsafe.Pointer(&lf.FaceName[0])), 28},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 偏移 = %d，应为 %d", c.name, c.got, c.want)
		}
	}
}

// 这些值全部照 Microsoft 文档抄，不是"看起来对"。
//
// 之所以专门写测试钉住：WM_HOTKEY 曾被写成 0x0303（那是 WM_QUERYDRAGICON），
// 结果热键注册成功、Windows 也发了消息，窗口过程却认不出来——按 Alt+E 毫无反应，
// 而且不报任何错。这类"常量错一位"的 bug 极难从现象反推，只能靠测试。
func TestWin32Constants(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"WM_HOTKEY", wmHotKey, 0x0312},
		{"WM_QUIT", wmQuit, 0x0012},
		{"WM_TIMER", wmTimer, 0x0113},
		{"WM_NCACTIVATE", wmNcActivate, 0x0086},
		{"WM_DPICHANGED", wmDpiChanged, 0x02E0},
		{"WM_CLOSE", wmClose, 0x0010},
		{"WM_VSCROLL", wmVScroll, 0x0115},
		{"WM_SIZE", wmSize, 0x0005},
		{"WM_GETOBJECT", wmGetObj, 0x003D},
		{"WM_COMMAND", wmCommand, 0x0111},
		{"WM_KEYDOWN", wmKeyDown, 0x0100},
		{"WM_DESTROY", wmDestroy, 0x0002},
		{"WM_NCCREATE", wmNcCreate, 0x0082},
		{"WM_NCDESTROY", wmNcDestroy, 0x0083},
		{"WM_GETTEXT", wmGetText, 0x000D},
		{"WM_GETTEXTLENGTH", wmGetTextLen, 0x000E},
		{"WM_SETTEXT", wmSetText, 0x000C},
		{"WM_SETFONT", wmSetFont, 0x0030},
		{"WM_GETFONT", wmGetFont, 0x0031},
		{"EM_SETSEL", emSetSel, 0x00B1},
		{"ES_MULTILINE", esMultiline, 0x0004},
		{"ES_AUTOVSCROLL", esAutoVScroll, 0x0040},
		{"ES_READONLY", esReadOnly, 0x0800},
		{"ES_WANTRETURN", esWantReturn, 0x1000},
		{"WS_TABSTOP", wsTabStop, 0x00010000},
		{"WS_POPUP", wsPopup, 0x80000000},
		{"WS_CHILD", wsChild, 0x40000000},
		{"WS_VISIBLE", wsVisible, 0x10000000},
		{"WS_VSCROLL", wsVScroll, 0x00200000},
		{"WS_EX_TOPMOST", wsExTopMost, 0x00000008},
		{"WS_EX_TOOLWINDOW", wsExToolWindow, 0x00000080},
		{"WS_EX_CLIENTEDGE", wsExClientEdge, 0x00000200},
		{"WS_EX_WINDOWEDGE", wsExWindowEdge, 0x00000100},
		{"WS_DISABLED", wsDisabled, 0x08000000},
		{"WS_CAPTION", wsBorder | 0x00400000, 0x00C00000},
		{"WS_GROUP", wsGroup, 0x00020000},
		{"CS_VREDRAW", csVRedraw, 0x0001},
		{"CS_HREDRAW", csHRedraw, 0x0002},
		{"SW_HIDE", swHide, 0},
		{"SW_SHOW", swShow, 5},
		{"SB_BOTTOM", sbBottomMsg, 7},
		{"SM_CXSCREEN", sm_CXScreen, 0},
		{"SM_CYSCREEN", sm_CYScreen, 1},
		{"COLOR_WINDOW", colorWindow, 10},
		{"MOD_ALT", modAlt, 0x0001},
		{"MOD_CONTROL", modControl, 0x0002},
		{"MOD_SHIFT", modShift, 0x0004},
		{"MOD_WIN", modWin, 0x0008},
		{"MOD_NOREPEAT", norepeat, 0x4000},
		{"VK_ESCAPE", vkEscape, 0x1B},
		{"VK_RETURN", vkReturn, 0x0D},
		{"NIF_MESSAGE", nifMessage, 0x0001},
		{"NIF_ICON", nifIcon, 0x0002},
		{"NIF_TIP", nifTip, 0x0004},
		{"NIF_INFO", nifInfo, 0x0010},
		{"NIM_ADD", nimAdd, 0},
		{"NIM_MODIFY", nimModify, 1},
		{"NIM_DELETE", nimDelete, 2},
		{"NIN_SELECT", ninSelect, 0x0400},
		{"TPM_RIGHTBUTTON", tpmRightButton, 0x0002},
		{"TPM_RETURNCMD", tpmReturnCmd, 0x0100},
		{"MF_STRING", mfString, 0},
		{"MF_SEPARATOR", mfSeparator, 0x0800},
		{"SW_SHOWNORMAL", swShowNormal, 1},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %#x，文档是 %#x", c.name, c.got, c.want)
		}
	}
}
