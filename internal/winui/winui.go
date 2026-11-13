// Package winui 是一个 Win32 原生小窗：Alt+E 呼出置顶弹窗，一个输入框 + 一个只读结果框。
//
// 为什么自己封 Win32：golang.org/x/sys/windows 这个包里只有零星几个 GUI 函数，
// WNDCLASSEX / LOGFONT / POINT / MSG 这些结构体都没有，也没有 CreateWindowEx 这类入口。
// 这里就按 Win32 文档自己定义，用 NewLazyDLL + NewProc 直接调 user32/gdi32，
// 换来的是冷启动 <100ms、exe 外面不用带任何 DLL。界面想更好看，下一步再换 WebView2。
package winui

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Options 由 main 填：查询函数、整句翻译、热键写法、自我退出时间。
type Options struct {
	Hotkey     string                      // 形如 "alt+e"
	Query      func(string) Result // 词典查询
	Translate  func(string) <-chan string  // 整句翻译，返回结果频道
	ConfigPath string                      // 托盘菜单里"打开配置文件"要用的路径
	Version    string                      // 版本号，显示在托盘菜单与提示文字里
	LogPath    string                      // 启动日志；空表示不写
	HideAfter  time.Duration               // >0：呼出后这么久自动退出，冒烟测试用
	Selfcheck  bool                        // 把每步 Win32 调用的成败都打出来，然后退出（不在自己的桌面里跑时好用）
	Debug      bool
}

// ---- Win32 常量：取自媒体文档的值，不依赖任何库的命名习惯 ----

const (
	csVRedraw = 0x00000001
	csHRedraw = 0x00000002
	csOwnDC   = 0x00000020
	csClassDC = 0x00000040
	csGlobal  = 0x00040000
	csClass   = csHRedraw | csVRedraw // 重绘提示，弹窗默认就这么点

	wsPopup        = 0x80000000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsVScroll      = 0x00200000
	wsDisabled     = 0x08000000
	wsBorder       = 0x00800000
	wsTabStop      = 0x00010000
	wsGroup        = 0x00020000
	wsNoPrefix     = 0x00000200
	wsExClientEdge = 0x00000200
	wsExTopMost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExWindowEdge = 0x00000100

	esMultiline   = 0x00000004
	esAutoVScroll = 0x00000040
	esReadOnly    = 0x00000800
	esWantReturn  = 0x00001000

	swHide       = 0
	swShow       = 5
	swShowNormal = 1

	wmDestroy     = 0x0002
	wmNcCreate    = 0x0082
	wmNcDestroy   = 0x0083
	wmNcActivate  = 0x0086
	wmSetText     = 0x000C
	wmSetFont     = 0x0030
	wmGetFont     = 0x0031
	wmGetText     = 0x000D
	wmGetTextLen  = 0x000E
	wmTimer       = 0x0113
	wmCommand     = 0x0111
	wmKeyDown     = 0x0100
	wmHotKey      = 0x0312
	wmCut         = 0x0300
	wmCopy        = 0x0301
	wmPaste       = 0x0302
	emGetSel      = 0x00B0
	cfUnicodeText = 13

	// 右键菜单项编号
	cmdCut       = 2001
	cmdCopy      = 2002
	cmdPaste     = 2003
	cmdSelectAll = 2004
	cmdAutoStart = 2005
	mfGrayed     = 0x00000001
	mfChecked    = 0x00000008
	wmClose      = 0x0010
	wmQuit       = 0x0012
	wmSize       = 0x0005
	wmDpiChanged = 0x02E0
	wmApp        = 0x0400
	wmGetObj     = 0x003D
	wmNcHitTest  = 0x0084

	sbBottom    = 0x0007 // WM_VSCROLL 的滚动请求：滚到底
	sbLineUp    = 0x0001
	sbLineDown  = 0x0002
	wmVScroll   = 0x0115
	wmKeydownU  = 0x0100
	sbPageDown  = 0x0006
	sbBottomMsg = 0x0007
	emSetSel    = 0x00B1

	sm_CXScreen = 0
	sm_CYScreen = 1

	colorWindow    = 10 // COLOR_WINDOW，作为窗口背景刷子即 COLOR_WINDOW+1
	defaultCharSet = 1  // LOGFONT 的字符集：DEFAULT_CHARSET

	outTTPrecis      = 4   // OUT_TT_PRECIS：优先 TrueType 轮廓
	clearTypeQuality = 5   // CLEARTYPE_QUALITY：次像素平滑，小字号不发毛
	fontWeightNormal = 400 // FW_NORMAL
	fontWeightSemiBold = 600 // FW_SEMIBOLD，卡片标题用
	lfFaceSize       = 32
	norepeat         = 0x4000 // MOD_NOREPEAT

	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004
	modWin     = 0x0008

	vkControl = 0x11
	vkA       = 0x41
	vkEscape  = 0x1B
	vkReturn  = 0x0D

	hotID      = 1 // 我们自己那张热键的编号
	timerPoll  = uintptr(2)
	timerQuit  = uintptr(1)
	pollMs     = 70
	idleForLlm = 5 // 文本静止这么久才去联网翻整句（约 350ms）

	// WM_APP 之后是我们自己的消息
	wmLLMReply = wmApp + 1 // 整句翻译回来了
	wmTray     = wmApp + 2 // 托盘图标被点了
)

// ---- 自己按文档定义的结构体（amd64 布局，与 C 一致）----

// wndClassW 是 WNDCLASSW（x64 上 72 字节）。
//
// 为什么不用 WNDCLASSEXW：实测这台机器上 RegisterClassExW 对任何 cbSize（72/76/80/84/88）
// 都返回 ERROR_INVALID_PARAMETER，而同样数据的 RegisterClassW 稳定成功。结构体字节我逐字段
// 核对过是对的，所以不是布局问题，就是 Ex 版本在这台机器上不可用。
// 我们不需要 WNDCLASSEX 多出来的 cbSize 与 hIconSm（无边框弹窗没有标题栏小图标），
// 所以用老 API 完全够，而且更稳。
type wndClassW struct {
	Style      uint32  // 0
	WndProc    uintptr // 8
	ClsCount   int32   // 16
	WndExtra   int32   // 20
	HInstance  uintptr // 24
	Icon       uintptr // 32
	Cursor     uintptr // 40
	Background uintptr // 48
	MenuName   *uint16 // 56
	ClassName  *uint16 // 64
} // 72

// logFont 是 LOGFONTW（x64，sizeof = 92）。
//
// 字段顺序必须和 C 完全一致。这里曾经漏掉 lfWeight/lfItalic/lfUnderline/lfStrikeOut 四个字段，
// 整个结构体错位 4 字节，后果是：lfWeight 被读成 1（极细字重）、lfQuality 读到字体名里的一个字节
// （非法值，退回 DEFAULT_QUALITY，ClearType 失效）、lfFaceName 从错位的偏移读成 "crosoft YaHei UI"。
// 也就是说字体名、字重、平滑方式**全都没生效**，调字号当然看不出改善。
type logFont struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32 // 400 = FW_NORMAL
	Italic         uint8
	Underline      uint8
	StrikeOut      uint8
	CharSet        uint8
	OutPrecision   uint8
	ClipPrecision  uint8
	Quality        uint8
	PitchAndFamily uint8
	FaceName       [lfFaceSize]uint16
}

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// ---- 调 Win32：x/sys 的 Proc.Call 不告诉我哪个返回值代表失败，这里按每个 API 的语义自己判 ----

var (
	user32   = windows.NewLazyDLL("user32.dll")
	gdi32    = windows.NewLazyDLL("gdi32.dll")
	kernel32 = windows.NewLazyDLL("kernel32.dll")

	procRegisterClass   = user32.NewProc("RegisterClassW")
	procCreateWindow    = user32.NewProc("CreateWindowExW")
	procShow            = user32.NewProc("ShowWindow")
	procDestroy         = user32.NewProc("DestroyWindow")
	procSetPos          = user32.NewProc("SetWindowPos")
	procClipFormatAvail = user32.NewProc("IsClipboardFormatAvailable")
	procGetMetrics      = user32.NewProc("GetSystemMetrics")
	procGetCursor       = user32.NewProc("GetCursorPos")
	procForeground      = user32.NewProc("SetForegroundWindow")
	procGetForeground   = user32.NewProc("GetForegroundWindow")
	procGetWndThread    = user32.NewProc("GetWindowThreadProcessId")
	procAttachInput     = user32.NewProc("AttachThreadInput")
	procSetFocus        = user32.NewProc("SetFocus")
	procSetText         = user32.NewProc("SetWindowTextW")
	procGetText         = user32.NewProc("GetWindowTextW")
	procGetTextLen      = user32.NewProc("GetWindowTextLengthW")
	procSend            = user32.NewProc("SendMessageW")
	procPost            = user32.NewProc("PostMessageW")
	procRegHot          = user32.NewProc("RegisterHotKey")
	procUnregHot        = user32.NewProc("UnregisterHotKey")
	procSetTimer        = user32.NewProc("SetTimer")
	procKillTimer       = user32.NewProc("KillTimer")
	procAsyncKey        = user32.NewProc("GetAsyncKeyState")
	procDefProc         = user32.NewProc("DefWindowProcW")
	procGetMsg          = user32.NewProc("GetMessageW")
	procTranslate       = user32.NewProc("TranslateMessage")
	procDispatch        = user32.NewProc("DispatchMessageW")
	procQuit            = user32.NewProc("PostQuitMessage")
	procDpi             = user32.NewProc("GetDpiForWindow")
	procGetRect         = user32.NewProc("GetWindowRect")
	procGetDesk         = user32.NewProc("GetThreadDesktop")
	procOpenDesk        = user32.NewProc("OpenDesktopW")
	procSetDesk         = user32.NewProc("SetThreadDesktop")
	procThreadID        = kernel32.NewProc("GetCurrentThreadId")
	procCreateFont      = gdi32.NewProc("CreateFontIndirectW")
	procDpiAware        = user32.NewProc("SetProcessDPIAware")
)

// call 把 Call 的三返回值收成两元组：主返回值 + 错误。
// LazyProc.Call 的错误永远是 non-nil（它照着 GetLastError 造的），所以调用方必须看主返回值。
func call(p *windows.LazyProc, a ...uintptr) (uintptr, error) {
	r, _, e := p.Call(a...)
	return r, e
}

type handle = uintptr

func (wc *wndClassW) cbSize() uint32 { return uint32(unsafe.Sizeof(*wc)) }

// ---- 应用状态 ----

type appState struct {
	hwnd      handle
	hEdit     handle
	hResult   handle
	fontBig   handle
	fontBold  handle
	fontSm    handle
	fontSmall handle

	query     func(string) Result
	translate func(string) <-chan string
	hideAfter time.Duration
	debug     bool

	hotMods uint32
	hotVK   uint32
	hotName string

	dpi        int
	pending    string // 上一次查询用的文本
	gen        int    // 查询轮次；翻译回来时用它丢掉过期结果
	llmSent    bool
	active     bool    // 窗口是否真的激活过（用来区分"刚显示"与"被切走"）
	wantLLM    bool    // 当前文本是否真的需要联网翻译
	result     Result      // 当前结果
	boxRects   []hitTarget // 主翻译圆角框的矩形与要复制的内容
	hoverBox   int         // 鼠标悬停的框序号，-1 表示没有
	btnHover   bool        // 鼠标悬停在「AI 翻译」按钮上
	btnDown    bool        // 「AI 翻译」按钮被按下
	keyWas     [3]bool     // 0=回车 1=ESC 2=Ctrl+A 的上一帧状态

	mu      sync.Mutex
	results []llmReply
}

type llmReply struct {
	gen  int
	text string
}

var app = &appState{}

// Run 建窗口、装热键，然后进消息循环，直到窗口被关掉。
// Selfcheck 模式下只把每一步的成败打出来就返回：在没有自己桌面的会话里（比如被 pwsh 拉起来跑）
// 建不出窗口时，这是唯一能把"到底哪一步失败"说清楚的办法。
func Run(o Options) (err error) {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("winui 只支持 Windows")
	}
	// 日志先开：后面任何一步失败都要留下现场
	startLog(o.LogPath)
	defer closeLog()
	initTheme()
	defer func() {
		if err != nil {
			logLine("失败: " + err.Error())
		}
	}()

	// 单实例：双击两次不该出现两个托盘图标，第二个还会因为热键被占而报一句看不懂的错。
	// 已经在跑的时候，让先起来的那个把窗口弹出来——用户看到的是词典，而不是黑框一闪。
	if !o.Selfcheck {
		if ok := claimSingleInstance(); !ok {
			if signalRunningInstance() {
				fmt.Println("mgdic 已经在运行，已让它把窗口弹出来。")
				logLine("已有实例在运行，已请求它显示窗口")
			} else {
				fmt.Println("mgdic 已经在运行（看任务栏托盘图标）。")
				logLine("已有实例在运行，退出")
			}
			return nil
		}
	}
	// 弹窗与热键、计时器、消息循环必须在同一条线程上；这条线程还必须挂着桌面才能注册类与建窗。
	runtime.LockOSThread()
	procDpiAware.Call()

	app.query, app.translate = o.Query, o.Translate
	app.hoverBox = -1
	app.hideAfter, app.debug = o.HideAfter, o.Debug

	say := func(s string) { logLine(s) }

	// 先把所有 Win32 导出解析一遍：名字写错时 LazyProc 会 panic，
	// 那样程序会一路跑到消息循环才崩，看起来就是"闪退"。
	if err := checkProcs(); err != nil {
		return err
	}
	say("Win32 导出全部解析成功")
	registerShowMsg()
	say(fmt.Sprintf("跨进程显示消息号 = %#x", showMsg))

	desktopNote := attachThreadDesktop()
	say("桌面: " + desktopNote)
	say("热键写法: " + o.Hotkey)

	mods, vk, name, err := parseHotkey(o.Hotkey)
	if err != nil {
		return err
	}
	app.hotMods, app.hotVK, app.hotName = mods, vk, name

	cls := utf16("mgdic.Popup")
	if len(cls) == 0 {
		return fmt.Errorf("窗口类名不是有效的 UTF-16")
	}
	wc := wndClassW{
		Style:      csClass,
		WndProc:    windows.NewCallback(wndProc),
		Background: uintptr(colorWindow + 1), // 文档：hbrBackground < 16 当颜色索引处理
		ClassName:  &cls[0],
	}
	atom, rcErr := call(procRegisterClass, pptr(&wc))
	say(fmt.Sprintf("RegisterClassW（%d 字节）-> atom=%#x  %v", wc.cbSize(), atom, rcErr))
	runtime.KeepAlive(&wc)
	runtime.KeepAlive(&cls)
	if atom == 0 {
		return fmt.Errorf("注册窗口类失败: %w", rcErr)
	}
	_ = atom

	app.dpi = 96
	l := layoutFor(app.dpi)
	hwnd, err := createWindow(0, cls, "mgdic", wsPopup, l, 0)
	say(fmt.Sprintf("CreateWindowEx 弹窗 -> %#x  %v", hwnd, err))
	if err != nil {
		return fmt.Errorf("创建窗口失败: %w", err)
	}
	app.hwnd = hwnd
	applyShape(l.W, l.H, l.Radius)

	app.fontBig = fontFor(app.dpi, 15)
	app.fontBold = fontForWeight(app.dpi, 12, fontWeightSemiBold)
	app.fontSmall = fontFor(app.dpi, 10)
	app.fontSm = fontFor(app.dpi, 12)
	say(fmt.Sprintf("CreateFontIndirect 字体 -> %#x / %#x", app.fontBig, app.fontSm))

	edit, err := createWindow(app.hwnd, utf16("EDIT"), "", wsChild|wsVisible|wsTabStop|esWantReturn, l, 1)
	if err != nil {
		return fmt.Errorf("创建输入框失败: %w", err)
	}
	app.hEdit = edit

	// 结果区：RichEdit 只读文本框。用它而不是普通 EDIT，是因为 RichEdit 支持
	// 拖选、Ctrl+C、右键菜单，而且背景色可以用 EM_SETBKGNDCOLOR 控制。
	res, rerr := createRichResult(app.hwnd, l)
	if rerr != nil {
		return fmt.Errorf("创建结果框失败: %w", rerr)
	}
	app.hResult = res
	say(fmt.Sprintf("输入框=%#x 结果框=%#x（RichEdit，可拖选复制）", app.hEdit, app.hResult))

	// 输入框的灰色提示用 cue banner，不要拿窗口标题当占位文字——
	// 那样文字会成为真实内容，还会被当成一次查询（"输入框"被当中文查了）。
	setCueBanner(app.hEdit, "输入英文或中文，ESC 收起")

	setFont(app.hwnd, app.fontBig)
	setFont(app.hEdit, app.fontBig)
	setFont(app.hResult, app.fontSm)
	// 注意：不要给 RichEdit 发 EM_SETBKGNDCOLOR —— 实测这里直接访问违例崩溃。
	// 背景色交给 WM_CTLCOLOREDIT（ctlColor）处理。
	setResultText(placeholderText)
		layoutResult()

	if r, err := call(procRegHot, app.hwnd, hotID, uintptr(app.hotMods), uintptr(app.hotVK)); r == 0 {
		say(fmt.Sprintf("RegisterHotKey mods=%#x vk=%#x -> 失败 %v", app.hotMods, app.hotVK, err))
		return fmt.Errorf("注册全局热键 %s 失败: %w", app.hotName, err)
	}
	say(fmt.Sprintf("RegisterHotKey %s -> ok", app.hotName))

	if o.Selfcheck {
		call(procUnregHot, app.hwnd, hotID)
		call(procDestroy, app.hwnd)
		return nil
	}

	// 托盘：唯一常驻可见的东西，也是正常退出入口
	if err := initTray(o.ConfigPath, o.Version); err != nil {
		say(fmt.Sprintf("Shell_NotifyIcon 失败: %v", err))
		return fmt.Errorf("挂托盘图标失败: %w", err)
	}
	say("托盘图标已挂上")
	showBalloon("mgdic 已在后台运行", "按 Alt+E 呼出词典；右键托盘图标可退出。")

	// 双击 exe 时不该留一个黑框在那儿；--selfcheck / --debug 下留着，方便看输出
	if !o.Debug {
		hideConsole()
	}

	// 轮询计时器必须一直开着：输入框的内容变化全靠它发现，跟 HideAfter 无关。
	// （这里曾经只在 HideAfter>0 时才启动，导致正常运行下打字永远不触发查询。）
	if r, _ := call(procSetTimer, app.hwnd, timerPoll, pollMs, 0); r == 0 {
		return fmt.Errorf("SetTimer(poll) 失败")
	}

	fmt.Printf("mgdic 已就绪：按 %s 呼出词典窗口；托盘图标右键可退出\n", app.hotName)
	if o.Debug {
		fmt.Printf("  热键 mods=%#x vk=%#x / 窗口 %#x / DPI %d\n", app.hotMods, app.hotVK, app.hwnd, app.dpi)
	}

	if o.HideAfter > 0 {
		showAtCursor()
		ms := uintptr(o.HideAfter / time.Millisecond)
		if r, _ := call(procSetTimer, app.hwnd, timerQuit, ms, 0); r == 0 {
			return fmt.Errorf("SetTimer(quit) 失败")
		}
	}

	var m msg
	for {
		r, _, _ := procGetMsg.Call(pptr(&m), 0, 0, 0)
		switch int32(r) {
		case -1:
			return fmt.Errorf("GetMessage 失败")
		case 0: // WM_QUIT
			goto done
		}
		procTranslate.Call(pptr(&m))
		procDispatch.Call(pptr(&m))
		if m.Message == wmQuit {
			break
		}
	}
done:
	// KeepAlive：GetMessageW 是同步调用，MSG 结构体在循环退出后才允许回收
	runtime.KeepAlive(&m)
	removeTray()
	call(procUnregHot, app.hwnd, hotID)
	call(procDestroy, app.hwnd)
	return nil
}

// singleInstance 持有那个命名互斥体；进程退出时由系统释放，所以不用显式关。
var singleInstance windows.Handle

// showMsg 是"请把窗口弹出来"的广播消息号，由 RegisterWindowMessageW 分配。
// 第二个实例靠它让已经在跑的那个实例显示窗口，而不是默默退出。
var showMsg uint32

const hwndBroadcast = 0xFFFF

// registerShowMsg 注册跨进程消息名。同名注册得到同一个消息号，所以两个实例能对上。
func registerShowMsg() {
	name := utf16("mgdic-show-dictionary")
	if len(name) == 0 {
		return
	}
	if r, _, _ := procRegisterMsg.Call(pptr(&name[0])); r != 0 {
		showMsg = uint32(r)
	}
	runtime.KeepAlive(&name)
}

// signalRunningInstance 广播一条显示请求，返回是否发出去了。
func signalRunningInstance() bool {
	registerShowMsg()
	if showMsg == 0 {
		return false
	}
	r, _, _ := procPost.Call(hwndBroadcast, uintptr(showMsg), 0, 0)
	return r != 0
}

// claimSingleInstance 保证只跑一个实例。拿不到互斥体时返回 true：
// 宁可多开一个，也别因为一次 API 失败就把用户挡在门外（后面注册热键还会兜一次底）。
func claimSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(`Local\mgdic-single-instance`)
	if err != nil {
		return true
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == nil {
		singleInstance = h
		return true
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return false
	}
	return true
}

// attachThreadDesktop 把当前线程挂到进程所在的桌面上。// Go 运行时创建的线程不继承桌面，而注册类与建窗都要桌面；换桌面句柄只在线程还没建过窗口时管用，
// 所以必须赶在建窗之前。已经在自己桌面上（用户从资源管理器双击 exe 的常规情形）就直接跳过。
// attachThreadDesktop 只在"这条线程真的没有桌面"时，才把它挂到当前窗口站的 Default 桌面上。
//
// 这里曾经有两个错，合起来会让 RegisterClassEx 在正常桌面上也报"参数不正确"：
//  1. 检测时把 GetCurrentThread 的伪句柄 (-2) 当成了线程 ID 传给 GetThreadDesktop。
//     GetThreadDesktop 要的是 GetCurrentThreadId() 返回的 DWORD，所以它永远失败，
//     于是每条线程都被误判成"没有桌面"。
//  2. 误判之后用 OpenDesktopW(..., 0) 打开桌面——零权限句柄。拿这种句柄去注册窗口类，
//     就会被拒。
//
// 现在：用真实线程 ID 检测；真要挂，就带上桌面该有的权限，并回读确认。
func attachThreadDesktop() string {
	tid, _, _ := procThreadID.Call()
	before, _ := call(procGetDesk, tid)
	if before != 0 {
		return fmt.Sprintf("线程 %d 已有桌面 %#x，不动", tid, before)
	}
	name := utf16("Default")
	if len(name) == 0 {
		return "桌面名 Default 不是有效 UTF-16"
	}
	// DESKTOP_CREATEWINDOW|READOBJECTS|WRITEOBJECTS|ENUMERATE|HOOKCONTROL
	const need = 0x0002 | 0x0001 | 0x0080 | 0x0040 | 0x0008
	hdesk, e := call(procOpenDesk, pptr(&name[0]), 0, 0, need)
	if hdesk == 0 {
		// 权限要不到就退回最小权限，至少把句柄拿到
		hdesk, e = call(procOpenDesk, pptr(&name[0]), 0, 0, 0)
	}
	runtime.KeepAlive(&name)
	if hdesk == 0 {
		return "OpenDesktop(\"Default\") 失败: " + e.Error()
	}
	if r, e2 := call(procSetDesk, hdesk); r == 0 {
		return "SetThreadDesktop 失败: " + e2.Error()
	}
	after, _ := call(procGetDesk, tid)
	return fmt.Sprintf("线程 %d 桌面 %#x -> %#x（已挂上）", tid, before, after)
}

// createWindow 建弹窗或子控件。which: 0=主窗口, 1=输入框, 2=结果框。
// 主窗口以前误用了输入框的几何（于是建出来是 474×38），只有呼出时才被 SetWindowPos 纠正；
// 虽然看不出来，但那样窗口在隐藏期间尺寸是错的。
func createWindow(parent handle, cls []uint16, title string, style uint32, l layout, which int) (handle, error) {
	x, y, w, h := 0, 0, l.W, l.H
	switch which {
	case 1:
		x, y, w, h = l.editX(), l.editY(), l.editW(), l.editH()
	case 2:
		x, y, w, h = l.resX(), l.resY(), l.resW(), l.resH()
	}
	name := utf16(title)
	if len(cls) == 0 || len(name) == 0 {
		return 0, fmt.Errorf("窗口类名或窗口标题不是有效的 UTF-16")
	}
	r, e := call(procCreateWindow,
		wsExTopMost|wsExToolWindow, pptr(&cls[0]), pptr(&name[0]),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, 0, 0, 0)
	// KeepAlive：CreateWindowExW 是同步调用，返回之后才轮到这些缓冲区退休
	runtime.KeepAlive(&cls)
	runtime.KeepAlive(&name)
	if r == 0 {
		return 0, e
	}
	return r, nil
}

// layout 是弹窗与两个子控件的位置，全部按 DPI 缩放。
//
// 留白比"能用"要大一些：现代界面靠的是留白和字号，不是边框。
type layout struct {
	X, Y, W, H, Margin, EditH, Gap, Radius, Pad int
	BtnW, BtnH, BtnRadius                       int
}

func layoutFor(dpi int) layout {
	s := func(v int) int { return v * dpi / 96 }
	return layout{
		W:         s(620),
		H:         s(340),
		Margin:    s(16), // 窗口内边距
		Pad:       s(10), // 控件内部左右留白
		EditH:     s(38), // 输入框高度
		Gap:       s(12), // 输入框与分隔线、结果区之间
		Radius:    s(12),
		BtnW:      s(88),
		BtnH:      s(32),
		BtnRadius: s(8),
	}
}

// sepY 是输入框下方那条细分隔线的位置。
func (l layout) sepY() int { return l.Margin + l.EditH + l.Gap/2 }

// 输入框在左，按钮在右，中间留一个 Gap/2。
func (l layout) btnX() int  { return l.W - l.Margin - l.Pad - l.BtnW }
func (l layout) btnY() int  { return l.Margin + (l.EditH-l.BtnH)/2 }
func (l layout) btnW() int  { return l.BtnW }
func (l layout) btnH() int  { return l.BtnH }
func (l layout) editX() int { return l.Margin + l.Pad }
func (l layout) editY() int { return l.Margin }
func (l layout) editW() int { return l.btnX() - l.Gap/2 - l.editX() }
func (l layout) editH() int { return l.EditH }
func (l layout) resX() int  { return l.Margin + l.Pad }
func (l layout) resY() int  { return l.sepY() + l.Gap/2 + 1 }
func (l layout) resW() int  { return l.W - 2*(l.Margin+l.Pad) }
func (l layout) resH() int  { return l.H - l.resY() - l.Margin }

// fontFor 造一个 DPI 缩放、支持汉字的字体。
//
// 三个参数决定"字好不好看"：
//   - Quality = CLEARTYPE_QUALITY(5)：默认的 DEFAULT_QUALITY 在不少机器上不做次像素平滑，
//     小字号会显得又细又毛糙，这是之前字看着不干净的主因。
//   - OutPrecision = OUT_TT_PRECIS(4)：优先用 TrueType 轮廓，别让 GDI 退回点阵字体。
//   - 字体名用 Microsoft YaHei UI：它是为界面小字号设计的，比文档字体 Microsoft YaHei 更紧凑清晰。
func fontFor(dpi, pt int) handle {
	return fontForWeight(dpi, pt, fontWeightNormal)
}

// fontForWeight 同 fontFor，但可以指定字重（卡片标题用半粗体，跟正文区分开）。
func fontForWeight(dpi, pt, weight int) handle {
	lf := logFont{
		Height:       int32(-(pt * dpi / 72)),
		Weight:       int32(weight),
		CharSet:      defaultCharSet,
		OutPrecision: outTTPrecis,
		Quality:      clearTypeQuality,
	}
	name := "Microsoft YaHei UI"
	for i := 0; i < len(name) && i < lfFaceSize-1; i++ {
		lf.FaceName[i] = uint16(name[i])
	}
	h, _ := call(procCreateFont, pptr(&lf))
	// KeepAlive：CreateFontIndirectW 是同步调用，字体结构体读完才退休
	runtime.KeepAlive(&lf)
	if h == 0 {
		return 0
	}
	return h
}

func setFont(hwnd, font handle) {
	call(procSend, hwnd, wmSetFont, font, 1)
}

// setCueBanner 给单行输入框设灰色提示文字（EM_SETCUEBANNER）。
// 提示只是提示，不会变成内容，也不会触发查询。
func setCueBanner(hwnd handle, hint string) {
	const emSetCueBanner = 0x1501
	b := utf16(hint)
	if len(b) == 0 {
		return
	}
	call(procSend, hwnd, emSetCueBanner, 1, pptr(&b[0]))
	runtime.KeepAlive(&b)
}

// showAtCursor 在光标所在显示器上居中呼出，然后全选输入框等用户改。
func showAtCursor() {
	logLine("呼出窗口")
	l := layoutFor(app.dpi)
	x, y := centerOnMonitor(l.W, l.H)

	// SWP_NOZORDER|SWP_SHOWWINDOW：位置与大小都生效并显示。
	// 之前多带了一个 SWP_NOSIZE，等于把尺寸参数丢掉；少了 SHOWWINDOW 则要再单独 ShowWindow。
	call(procSetPos, app.hwnd, 0, uintptr(x), uintptr(y), uintptr(l.W), uintptr(l.H), 0x0004|0x0040)
	applyShape(l.W, l.H, l.Radius)
	call(procShow, app.hwnd, swShow)
	call(procInvalidateRect, app.hwnd, 0, 1)

	// 关键：光调 SetForegroundWindow 常常被系统拦下（不允许抢焦点），
	// 窗口于是"显示了但没拿到焦点"，紧接着又被 WM_NCACTIVATE(FALSE) 收起，
	// 用户看到的就是"按了 Alt+E 没反应"。先把自己挂到前台线程的输入队列上再抢。
	forceForeground()

	call(procSend, app.hEdit, emSetSel, 0, ^uintptr(0)) // EM_SETSEL(0,-1) = 全选
	app.pending, app.llmSent = "", false
	app.active = false // 还没真正激活，别因为收到失活消息就把窗口藏起来
	app.gen++
}

// forceForeground 把自己提到前台并让输入框拿到焦点。
// 系统只允许"前台进程"或"刚收到用户输入"的进程抢焦点，所以必要时临时把输入队列
// 挂到当前前台线程上（AttachThreadInput），抢完立刻摘掉。
func forceForeground() {
	me, _, _ := procThreadID.Call()
	fg, _ := call(procGetForeground)
	if fg != 0 {
		if fgTid, _ := call(procGetWndThread, fg, 0); fgTid != 0 && fgTid != me {
			call(procAttachInput, me, fgTid, 1)
			call(procForeground, app.hwnd)
			call(procSetFocus, app.hEdit)
			call(procAttachInput, me, fgTid, 0)
			return
		}
	}
	call(procForeground, app.hwnd)
	call(procSetFocus, app.hEdit)
}

func r2int(r uintptr) int32 { return int32(r) }

var _ = r2int

// wndProc 是窗口过程。弹窗全靠轮询计时器，不子类化控件，少一套 API 依赖。
func wndProc(hwnd, msgID, wparam, lparam uintptr) uintptr {
	switch msgID {
	case wmHotKey:
		// 同一个热键要能开关：窗口已经显示着就收起，否则居中弹出
		if visible := one(procIsWindowVisible, hwnd); visible != 0 {
			logLine("收到 WM_HOTKEY，窗口已显示 -> 收起")
			app.active = false
			call(procShow, hwnd, swHide)
			return 0
		}
		logLine(fmt.Sprintf("收到 WM_HOTKEY id=%d", wparam))
		showAtCursor()
		return 0

	case wmPaint:
		paintWindow(hwnd)
		return 0

	case wmLButtonDown:
		l := layoutFor(app.dpi)
		x, y := mouseXY(lparam)
		if inButton(l, x, y) {
			app.btnDown = true
			call(procInvalidateRect, hwnd, 0, 1)
		}
		return 0

	case wmLButtonUp:
		l := layoutFor(app.dpi)
		x, y := mouseXY(lparam)
		wasDown := app.btnDown
		app.btnDown = false
		call(procInvalidateRect, hwnd, 0, 1)
		if wasDown && inButton(l, x, y) {
			logLine("点击「AI 翻译」按钮")
			triggerTranslate()
			return 0
		}
		// 点主翻译的圆角框 = 复制那一块
		if i := hitBox(x, y); i >= 0 {
			copyText(app.boxRects[i].text, app.boxRects[i].text)
		}
		return 0

	case wmMouseMove:
		l := layoutFor(app.dpi)
		x, y := mouseXY(lparam)
		h := inButton(l, x, y)
		box := hitBox(x, y)
		if h != app.btnHover || box != app.hoverBox {
			app.btnHover, app.hoverBox = h, box
			call(procInvalidateRect, hwnd, 0, 1)
		}
		return 0

	case wmSetCursorMsg:
		// 鼠标在按钮或圆角框上时换成手型
		l := layoutFor(app.dpi)
		x, y := clientCursor(hwnd)
		if inButton(l, x, y) || hitBox(x, y) >= 0 {
			if cur := handCursor(); cur != 0 {
				procSetCursor.Call(cur)
				return 1
			}
		}

	case wmContextMenu:
		// 输入框自己不会弹右键菜单，它把 WM_CONTEXTMENU 转给父窗口。
		// lParam 是发出请求的子窗口句柄。
		if lparam == app.hEdit {
			showEditMenu(handle(lparam))
			return 0
		}

	case wmEraseBkgnd:
		// 返回 FALSE，让系统用窗口类的背景刷去擦。
		// 之前返回 TRUE 表示"我自己擦了"，但 FillRect 只要没生效，
		// 上一帧的文字就会残留在新内容下面（实测能看到旧词框的字）。
		return 0

	case wmCtlColorEdit, wmCtlColorStatic:
		return ctlColor(wparam)

	case wmTimer:
		switch wparam {
		case timerQuit:
			call(procPost, hwnd, wmClose, 0, 0)
		case timerPoll:
			poll(hwnd)
		}
		return 0

	case wmLLMReply: // 整句翻译回来了，wparam 是结果槽位
		takeReply(hwnd, int(wparam))
		return 0

	case wmTray: // 托盘图标被点了
		trayCallback(lparam)
		return 0

	case wmNcActivate:
		// 只在"真的激活过、现在又失活"时才收起。
		// 否则窗口刚显示、焦点还没拿到，系统先发来一个 WM_NCACTIVATE(FALSE)，
		// 窗口就被自己藏回去，看起来像按了热键没反应。
		if wparam != 0 {
			app.active = true
		} else if app.active {
			app.active = false
			logLine("失活，收起窗口")
			call(procShow, hwnd, swHide)
		}
		return 1 // TRUE：非客户区照常画

	case wmDpiChanged:
		// 系统把建议的 RECT 放在 lParam 上；那块内存是 Windows 分配的，GC 不认识它，
		// 指针方向也没人证得。这里干脆自己按 DPI 比例缩一遍当前矩形：
		// GetWindowRect 写的是 Go 自己的 rect 结构体，活命责任一目了然。
		old := int32(app.dpi)
		var r rect
		rr, _ := call(procGetRect, hwnd, pptr(&r))
		newDpi := int32(dpiOf(hwnd))
		if rr != 0 && old > 0 && newDpi > 0 {
			r.Left, r.Top = r.Left*newDpi/old, r.Top*newDpi/old
			r.Right, r.Bottom = r.Right*newDpi/old, r.Bottom*newDpi/old
			call(procSetPos, hwnd, 0, uintptr(int(r.Left)), uintptr(int(r.Top)),
				uintptr(int(r.Right-r.Left)), uintptr(int(r.Bottom-r.Top)), 0x0001|0x0004)
		}
		runtime.KeepAlive(&r)
		call(procShow, hwnd, swShow)
		app.dpi = int(newDpi)
		layoutChildren(layoutFor(app.dpi))
		applyShape(layoutFor(app.dpi).W, layoutFor(app.dpi).H, layoutFor(app.dpi).Radius)
		app.fontBig, app.fontSm = fontFor(app.dpi, 15), fontFor(app.dpi, 12)
		app.fontBold = fontForWeight(app.dpi, 12, fontWeightSemiBold)
		app.fontSmall = fontFor(app.dpi, 10)
		setFont(hwnd, app.fontBig)
		setFont(app.hEdit, app.fontBig)
		setFont(app.hResult, app.fontSm)
		return 0

	case wmClose:
		call(procQuit, 0)
		return 0
	}
	// Explorer 重启会广播 TaskbarCreated：把托盘图标挂回去，否则它会凭空消失
	if tray.taskbar != 0 && msgID == uintptr(tray.taskbar) {
		readdTray()
		return 0
	}
	// 第二个实例广播过来的"显示窗口"请求
	if showMsg != 0 && msgID == uintptr(showMsg) {
		showAtCursor()
		return 0
	}
	r, _ := call(procDefProc, hwnd, msgID, wparam, lparam)
	return r
}

// poll 每 70ms 跑一次：文本变了就查词典；回车翻译；ESC 收起。
//
// 为什么翻译改成"回车触发"而不是"静止就翻"：
//   - 打字过程中不会误触发联网，也就不会莫名其妙产生调用
//   - 用户明确按回车才花钱，心里有数
func poll(hwnd handle) {
	enterDown := highBit(procAsyncKey, vkReturn)
	escDown := highBit(procAsyncKey, vkEscape)
	enterEdge := enterDown && !app.keyWas[0]
	escEdge := escDown && !app.keyWas[1]
	app.keyWas[0], app.keyWas[1] = enterDown, escDown

	// 窗口没显示时不要响应全局的回车/ESC，否则会在别的程序里误触发
	if one(procIsWindowVisible, hwnd) == 0 {
		return
	}

	// Ctrl+A 全选。经典 EDIT 控件原生只支持 Ctrl+C/V/X，没有全选快捷键，得自己补。
	// 只在我们的窗口是前台窗口时响应，免得在别的程序里按 Ctrl+A 也把这里全选了。
	if fg, _ := call(procGetForeground); fg == hwnd {
		aDown := highBit(procAsyncKey, vkA)
		if aDown && !app.keyWas[2] && highBit(procAsyncKey, vkControl) {
			logLine("Ctrl+A 全选")
			call(procSend, app.hEdit, emSetSel, 0, ^uintptr(0))
		}
		app.keyWas[2] = aDown
	}

	if escEdge {
		logLine("ESC 收起")
		app.active = false
		call(procShow, hwnd, swHide)
		return
	}
	if enterEdge {
		// 回车 = 翻译，窗口不动。只在"这段文字需要翻译"时才动；
		// 想让单词也翻，点右边的「AI 翻译」按钮（它无条件翻译）。
		if app.wantLLM {
			logLine("回车触发翻译：" + app.pending)
			triggerTranslate()
		}
		return
	}

	text := editText(app.hEdit)
	if text == app.pending {
		return
	}
	app.pending, app.llmSent = text, false
	app.gen++
	app.hoverBox = -1
	if strings.TrimSpace(text) == "" {
		app.wantLLM = false
		app.result = Result{}
		setResultText(placeholderText)
		layoutResult()
		return
	}
	res := app.query(text)
	app.result = res
	app.wantLLM = res.NeedsLLM
	setResultText(infoText(res))
	layoutResult()
	call(procInvalidateRect, app.hwnd, 0, 1)
	logLine(fmt.Sprintf("查询 %q -> %d 张卡片，可翻译=%v", text, len(res.Cards), res.NeedsLLM))
}

// showEditMenu 在光标处给输入框/结果框弹右键菜单。
// 不可用的项（没选中时不能剪切复制、剪贴板没文字时不能粘贴）会变灰。
func showEditMenu(target handle) {
	menu, _, _ := procCreatePopup.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	// 有没有选中内容
	sel, _, _ := procSend.Call(target, emGetSel, 0, 0)
	start, end := int32(sel&0xFFFF), int32((sel>>16)&0xFFFF)
	hasSel := end > start
	// 剪贴板里有没有文字
	clip, _, _ := procClipFormatAvail.Call(cfUnicodeText)
	hasClip := clip != 0
	// 输入框永远可编辑，没有只读的情况
	readOnly := false

	addItem := func(id int, text string, enabled bool) {
		flags := uintptr(mfString)
		if !enabled {
			flags |= mfGrayed
		}
		label := utf16(text)
		procAppendMenu.Call(menu, flags, uintptr(id), pptr(&label[0]))
		runtime.KeepAlive(&label)
	}
	addItem(cmdCut, "剪切", hasSel && !readOnly)
	addItem(cmdCopy, "复制", hasSel)
	addItem(cmdPaste, "粘贴", hasClip && !readOnly)
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	addItem(cmdSelectAll, "全选", true)

	var p point
	procGetCursor.Call(pptr(&p))
	runtime.KeepAlive(&p)
	procForeground.Call(app.hwnd)
	cmd, _, _ := procTrackPopup.Call(menu,
		tpmRightButton|tpmReturnCmd,
		uintptr(p.X), uintptr(p.Y), 0, app.hwnd, 0)
	procPost.Call(app.hwnd, wmNull, 0, 0)

	switch cmd {
	case cmdCut:
		call(procSend, target, wmCut, 0, 0)
	case cmdCopy:
		call(procSend, target, wmCopy, 0, 0)
	case cmdPaste:
		call(procSend, target, wmPaste, 0, 0)
	case cmdSelectAll:
		call(procSend, target, emSetSel, 0, ^uintptr(0))
	}
	// 菜单关掉后把焦点还回去，接着打字
	call(procSetFocus, target)
}

// triggerTranslate 立即翻译输入框里的内容。
//
// 回车和「AI 翻译」按钮都走这里，区别只在调用点：
// 回车只在"这段文字需要翻译"时才动，按钮则是**任何时候都能强制翻**
// ——查完 apple 想听听 AI 怎么说，点按钮就行。
func triggerTranslate() {
	text := app.pending
	if strings.TrimSpace(text) == "" || app.llmSent {
		return
	}
	if app.translate == nil {
		app.result.Note = "没有配置 api_key：config.json 里的 api_key"
		setResultText(infoText(app.result))
		return
	}
	app.llmSent = true
	app.result.Note = "正在翻译…"
	setResultText(infoText(app.result))
	startTranslate(app.gen, text)
}

// mouseXY 从鼠标消息的 lParam 里取出客户区坐标（低字 x，高字 y，均为有符号）。
func mouseXY(lparam uintptr) (int, int) {
	return int(int16(lparam & 0xFFFF)), int(int16((lparam >> 16) & 0xFFFF))
}

// clientCursor 返回光标在客户区里的坐标。
func clientCursor(hwnd handle) (int, int) {
	var p point
	call(procGetCursor, pptr(&p))
	var r rect
	call(procGetRect, hwnd, pptr(&r))
	runtime.KeepAlive(&p)
	runtime.KeepAlive(&r)
	return int(p.X) - int(r.Left), int(p.Y) - int(r.Top)
}

// handCursor 加载一次手型光标，之后复用。
var handCursorOnce uintptr

func handCursor() uintptr {
	if handCursorOnce == 0 {
		handCursorOnce, _, _ = procLoadCursor.Call(0, idcHand)
	}
	return handCursorOnce
}

// startTranslate 起一个 goroutine 去调翻译，回来后用消息把结果送回界面线程。
func startTranslate(gen int, text string) {
	if app.translate == nil {
		return
	}
	go func(gen int, text string) {
		s := <-app.translate(text) // 频道只有一位，Translate 返回时就退出了
		app.mu.Lock()
		app.results = append(app.results, llmReply{gen: gen, text: s})
		idx := len(app.results) - 1
		app.mu.Unlock()
		call(procPost, app.hwnd, wmLLMReply, uintptr(idx), 0)
	}(gen, text)
}

// one 只要主返回值，不看错误：这些 API 的成败都得看返回值本身。
func one(p *windows.LazyProc, a ...uintptr) uintptr {
	r, _ := call(p, a...)
	return r
}

func highBit(p *windows.LazyProc, vk int) bool {
	r, _, _ := p.Call(uintptr(vk))
	return int16(r) < 0 // GetAsyncKeyState 的高位：按下即为负
}

func takeReply(hwnd handle, idx int) {
	app.mu.Lock()
	var rep llmReply
	if idx >= 0 && idx < len(app.results) {
		rep = app.results[idx]
	}
	app.mu.Unlock()
	if rep.gen != app.gen { // 用户已经改词了，过期结果丢掉
		return
	}
	if strings.TrimSpace(rep.text) == "" {
		rep.text = "（没翻出来）"
	}
	// 译文放在卡片下面的 Note 里，不动卡片本身
	app.result.Note = "[DeepSeek] " + rep.text
	setResultText(infoText(app.result))
}

func editText(hwnd handle) string {
	r, _ := call(procGetTextLen, hwnd)
	n := int(int32(r))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	call(procGetText, uintptr(hwnd), pptr(&buf[0]), uintptr(n+1))
	// KeepAlive：GetWindowTextW 是同步调用，缓冲区读完才退休
	runtime.KeepAlive(&buf)
	return windows.UTF16ToString(buf)
}

func layoutChildren(l layout) {
	call(procSetPos, app.hEdit, 0,
		uintptr(l.editX()), uintptr(l.editY()), uintptr(l.editW()), uintptr(l.editH()),
		0x0004|0x0010|0x0020)
	call(procSetPos, app.hResult, 0,
		uintptr(l.resX()), uintptr(l.resY()), uintptr(l.resW()), uintptr(l.resH()),
		0x0004|0x0010|0x0020)
}

func dpiOf(hwnd handle) int {
	r, _, _ := procDpi.Call(hwnd)
	if r == 0 {
		return 96
	}
	return int(r)
}

type ifaceWords struct{ typ, data uintptr }

// pptr 把指针交给 Win32：interface{} 的两个字里，第一个是类型，第二个才是那个指针本身。
// 只准传指针（含切片的 &s[0]）；传值的话 data 指向一份堆上的复制，读出来就没意义了。
// 真正的活命责任由调用方在 Win32 调用之后 runtime.KeepAlive 承担。
func pptr(p any) uintptr {
	i := *(*ifaceWords)(unsafe.Pointer(&p))
	runtime.KeepAlive(&p)
	return i.data
}

func utf16(s string) []uint16 {
	b, err := windows.UTF16FromString(s)
	if err != nil || len(b) == 0 {
		return nil
	}
	return b
}

// parseHotkey 解析 "alt+e" / "ctrl+shift+f3"。返回的 mods 已经带上 MOD_NOREPEAT。
func parseHotkey(s string) (mods uint32, vk uint32, name string, err error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, 0, "", fmt.Errorf("热键没配置")
	}
	for _, part := range strings.Split(s, "+") {
		switch part = strings.TrimSpace(part); part {
		case "alt":
			mods |= modAlt
		case "ctrl", "control":
			mods |= modControl
		case "shift":
			mods |= modShift
		case "win", "windows":
			mods |= modWin
		default:
			vk, err = vkOf(part)
			if err != nil {
				return 0, 0, "", err
			}
		}
	}
	if vk == 0 {
		return 0, 0, "", fmt.Errorf("热键缺少主键: %q", s)
	}
	return mods | norepeat, vk, s, nil
}

func vkOf(k string) (uint32, error) {
	if len(k) == 1 {
		c := k[0]
		switch {
		case c >= 'a' && c <= 'z':
			return uint32(c - 'a' + 'A'), nil
		case c >= '0' && c <= '9':
			return uint32(c), nil
		}
	}
	if strings.HasPrefix(k, "f") && len(k) > 1 {
		if n, e := strconv.Atoi(k[1:]); e == nil && n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), nil
		}
	}
	switch k {
	case "space":
		return 0x20, nil
	case "escape", "esc":
		return vkEscape, nil
	case "return", "enter":
		return vkReturn, nil
	case "comma", ",":
		return 0xBC, nil
	case "period", ".":
		return 0xBE, nil
	}
	return 0, fmt.Errorf("不认识的热键主键: %q", k)
}
