package winui

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 界面外观：配色、圆角、绘制、居中。
//
// 目标是"现代"一点：无边框圆角窗、扁平配色、去掉 Win32 默认那种 3D 凹陷边框、
// 跟随系统深色模式。全部用 GDI 画，不引入任何渲染引擎。

const (
	wmPaint          = 0x000F
	wmEraseBkgnd     = 0x0014
	wmCtlColorEdit   = 0x0133
	wmCtlColorStatic = 0x0138
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmMouseMove      = 0x0200
	wmMouseWheel     = 0x020A
	wmSetCursorMsg   = 0x0020

	monitorDefaultToNearest = 0x00000002

	nullBrush = 5 // GetStockObject(NULL_BRUSH)，空心画刷，用来只画描边
	nullPen   = 8 // GetStockObject(NULL_PEN)，空心画笔，用来只填色不描边
	psSolid   = 0 // 实线画笔

	idcHand = 32649 // IDC_HAND

	bkTransparent = 1 // SetBkMode 的 TRANSPARENT：文字背景不刷色
)

const (
	dtLeft       = 0x0000
	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtWordBreak  = 0x0010
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800
	dtCalcRect   = 0x0400
)

// 按钮配色（COLORREF = 0x00BBGGRR）
const (
	btnAccent      = 0x00EB6F2F // #2F6FEB
	btnAccentHover = 0x00C45925 // #2559C4
	btnAccentDown  = 0x00A84A1E // #1E4AA8
	btnText        = 0x00FFFFFF
)

// paintStruct 是 PAINTSTRUCT（x64，sizeof = 72）。
type paintStruct struct {
	Hdc         uintptr
	FErase      int32
	_           int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

// monitorInfo 是 MONITORINFO（x64，sizeof = 40）。
type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

var (
	dwmapi  = windows.NewLazyDLL("dwmapi.dll")
	msimg32 = windows.NewLazyDLL("msimg32.dll")

	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfo   = user32.NewProc("GetMonitorInfoW")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procSetWindowRgn     = user32.NewProc("SetWindowRgn")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procInvalidateRect   = user32.NewProc("InvalidateRect")
	procFillRect         = user32.NewProc("FillRect")
	procSetTextColor     = gdi32.NewProc("SetTextColor")
	procSetBkColor       = gdi32.NewProc("SetBkColor")
	procSetBkMode        = gdi32.NewProc("SetBkMode")
	procAlphaBlend       = msimg32.NewProc("AlphaBlend")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procCreatePen        = gdi32.NewProc("CreatePen")
	procSelectObject     = gdi32.NewProc("SelectObject")
	procGetStockObject   = gdi32.NewProc("GetStockObject")
	procRoundRect        = gdi32.NewProc("RoundRect")
	procCreateRoundRgn   = gdi32.NewProc("CreateRoundRectRgn")
	procDrawText         = user32.NewProc("DrawTextW")
	procLoadCursor       = user32.NewProc("LoadCursorW")
	procSetCursor        = user32.NewProc("SetCursor")
	procIntersectClip    = gdi32.NewProc("IntersectClipRect")
	procSelectClipRgn    = gdi32.NewProc("SelectClipRgn")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
	procCreateCompatBM  = gdi32.NewProc("CreateCompatibleBitmap")
	procBitBlt          = gdi32.NewProc("BitBlt")
	procGetDC           = user32.NewProc("GetDC")
	procReleaseDC       = user32.NewProc("ReleaseDC")
	procDwmSetAttribute  = dwmapi.NewProc("DwmSetWindowAttribute")
)

// placeholderText 是空状态下画在结果区的使用提示。
// 不能用 EM_SETCUEBANNER：那个提示在控件拿到焦点时就消失，而输入框一直是焦点状态。
const placeholderText = "输入英文或中文，边打边查\n\n" +
	"· 单词、词组、词形变化、拼写纠错 —— 全部离线\n" +
	"· 整句按回车翻译；单词想翻就点右边的「AI 翻译」\n\n" +
	"ESC 收起　·　再按一次 Alt+E 也能收起"

// theme 是当前生效的一套颜色。
type theme struct {
	Bg         uint32 // COLORREF，0x00BBGGRR
	Text       uint32
	Hint       uint32
	Border     uint32
	Accent     uint32
	CardBg     uint32
	CardHover  uint32
	CardBorder uint32
	brBg       uintptr
	brBorder   uintptr
	brEdit     uintptr
	Dark       bool
}

var th theme

// initTheme 按系统深浅色选一套配色，并建好画刷。
func initTheme() {
	th = theme{
		Bg:         0x00FFFFFF, // #FFFFFF
		Text:       0x0028231F, // #1F2328
		Hint:       0x009E948B, // #8B949E
		Border:     0x00EBE7E5, // #E5E7EB
		Accent:     0x00EB6F2F, // #2F6FEB
		CardBg:     0x00FAF8F7, // #F7F8FA，比窗口底色略深一点点
		CardHover:  0x00F4F1F0, // #F0F1F4
		CardBorder: 0x00EBE7E5, // #E5E7EB
	}
	if systemUsesDark() {
		th.Dark = true
		th.Bg = 0x00221F1E          // #1E1F22
		th.Text = 0x00E6E6E6        // #E6E6E6
		th.Hint = 0x008B8B8B        // #8B8B8B
		th.Border = 0x003E3B3A      // #3A3B3E
		th.CardBg = 0x00292523      // #232529
		th.CardHover = 0x00302C2A   // #2A2C30
		th.CardBorder = 0x003E3B3A  // #3A3B3E
	}
	th.brBg, _, _ = procCreateSolidBrush.Call(uintptr(th.Bg))
	th.brBorder, _, _ = procCreateSolidBrush.Call(uintptr(th.Border))
	th.brEdit, _, _ = procCreateSolidBrush.Call(uintptr(th.Bg))
}

// systemUsesDark 读注册表里的系统主题设置；读不到就当浅色。
func systemUsesDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return v == 0
}

// paintWindow 画窗口。
//
// 用双缓冲：先把整帧画到内存位图，再一次性 BitBlt 到窗口。
// 之前直接画到窗口 DC，结果 FillRect 没擦干净时上一帧的文字会残留
// （实测能看到旧词框的笔画漏在新内容下面）。双缓冲从根上消除这类问题，也顺带消掉闪烁。
func paintWindow(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, pptr(&ps))
	runtime.KeepAlive(&ps)
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, pptr(&ps))

	var rc rect
	procGetClientRect.Call(hwnd, pptr(&rc))
	runtime.KeepAlive(&rc)
	w, h := int(rc.Right), int(rc.Bottom)
	if w <= 0 || h <= 0 {
		return
	}

	mem, _, _ := procCreateDC.Call(hdc)
	if mem == 0 {
		return
	}
	defer procDeleteDC.Call(mem)
	bm, _, _ := procCreateCompatBM.Call(hdc, uintptr(w), uintptr(h))
	if bm == 0 {
		return
	}
	oldBM, _, _ := procSelectObject.Call(mem, bm)

	paintScene(mem, rc)

	// 必须先把内容贴过去，再取消选中位图——顺序反了的话 BitBlt 读的是
	// 源 DC 里默认那张 1×1 单色位图，贴出来就是空白。
	//
	// 贴之前还要清掉裁剪区：BeginPaint 会把 DC 裁到"无效区域"，
	// 只重绘了那部分，其余像素仍是上一帧的，于是旧文字会漏在新内容旁边。
	procSelectClipRgn.Call(hdc, 0)
	const srcCopy = 0x00CC0020
	procBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), mem, 0, 0, srcCopy)

	procSelectObject.Call(mem, oldBM)
	procDeleteObject.Call(bm)
}

// paintScene 把一帧画到给定 DC 上（不关心它是不是窗口 DC）。
func paintScene(hdc uintptr, rc rect) {
	l := layoutFor(app.dpi)

	// 底色
	procFillRect.Call(hdc, pptr(&rc), th.brBg)

	// 输入框下面一条细分隔线
	sep := rect{
		Left:   int32(l.Margin),
		Top:    int32(l.sepY()),
		Right:  int32(l.W - l.Margin),
		Bottom: int32(l.sepY() + 1),
	}
	procFillRect.Call(hdc, pptr(&sep), th.brBorder)
	runtime.KeepAlive(&sep)

	// 1px 描边
	drawRoundBorder(hdc, rect{Right: int32(l.W), Bottom: int32(l.H)}, l.Radius, th.Border)

	// 主翻译的圆角框（自己画，点一下复制）
	paintBoxes(hdc, l)

	// 下方辅助信息是 RichEdit 只读文本框（可拖选复制），窗口只负责底色、
	// 分隔线和「AI 翻译」按钮。
	paintButton(hdc, l)
}

// paintButton 画输入框右侧那个「AI 翻译」按钮。
//
// 它和回车的区别：回车只在"这段文字需要翻译"时才动（命中词条的单词不动），
// 而这个按钮**任何时候都能强制翻译**——查完 apple 想听听 AI 怎么说，点它就行。
// 不用 BUTTON 控件：系统按钮是灰色 3D 的，跟这套扁平配色不搭；自己画反而简单。
func paintButton(hdc uintptr, l layout) {
	rc := rect{
		Left:   int32(l.btnX()),
		Top:    int32(l.btnY()),
		Right:  int32(l.btnX() + l.btnW()),
		Bottom: int32(l.btnY() + l.btnH()),
	}
	bg := uint32(btnAccent)
	switch {
	case app.btnDown:
		bg = btnAccentDown
	case app.btnHover:
		bg = btnAccentHover
	}
	text := uint32(btnText)

	// 抗锯齿圆角（GDI 的 RoundRect 没有抗锯齿，圆角会有一圈阶梯）
	drawRoundRectAA(hdc, l.btnX(), l.btnY(), l.btnW(), l.btnH(), l.BtnRadius, bg)

	// DrawText 默认是不透明背景模式：它会把文字区域先用背景色（白）刷一遍。
	// 按钮文字是白色，于是白字盖在白底上——看起来就是"按钮上没有字"。
	procSetBkMode.Call(hdc, bkTransparent)
	procSetTextColor.Call(hdc, uintptr(text))
	if app.fontSm != 0 {
		procSelectObject.Call(hdc, app.fontSm)
	}
	label := utf16("AI 翻译")
	if len(label) == 0 {
		return
	}
	procDrawText.Call(hdc, pptr(&label[0]), ^uintptr(0), pptr(&rc),
		dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)
	runtime.KeepAlive(&label)
	runtime.KeepAlive(&rc)
}

// blendFunction 是 BLENDFUNCTION（4 字节）。
type blendFunction struct {
	BlendOp             uint8
	BlendFlags          uint8
	SourceConstantAlpha uint8
	AlphaFormat         uint8
}

const (
	acSrcOver   = 0
	acSrcAlpha  = 1
	superSample = 4 // 每个像素 4x4 个子采样点
)

// drawRoundRectAA 画一个**抗锯齿**的圆角矩形。
//
// 为什么不用 RoundRect：GDI 的填充不做抗锯齿，圆角上会有一圈明显的阶梯。
// 这里自己在 Go 里按 4×4 超采样算出每个像素的覆盖率，做成 32 位带 alpha 的位图，
// 再用 AlphaBlend 混合上去——边缘就是平滑的。
func drawRoundRectAA(hdc uintptr, x, y, w, h, radius int, color uint32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	px := make([]uint32, w*h)
	r8 := float64(color & 0xFF)         // R
	g8 := float64((color >> 8) & 0xFF)  // G
	b8 := float64((color >> 16) & 0xFF) // B

	sub := float64(superSample)
	for py := 0; py < h; py++ {
		for pxi := 0; pxi < w; pxi++ {
			inside := 0
			for sy := 0; sy < superSample; sy++ {
				for sx := 0; sx < superSample; sx++ {
					fx := float64(pxi) + (float64(sx)+0.5)/sub
					fy := float64(py) + (float64(sy)+0.5)/sub
					if inRoundRectF(fx, fy, 0, 0, float64(w), float64(h), float64(radius)) {
						inside++
					}
				}
			}
			if inside == 0 {
				continue
			}
			a := uint32(inside) * 255 / (superSample * superSample)
			// 32bpp DIB 的内存顺序是 B,G,R,A；AlphaBlend 要预乘 alpha
			bb := uint32(b8*float64(a)/255) & 0xFF
			gg := uint32(g8*float64(a)/255) & 0xFF
			rr := uint32(r8*float64(a)/255) & 0xFF
			px[py*w+pxi] = bb | gg<<8 | rr<<16 | a<<24
		}
	}

	hdcMem, _, _ := procCreateDC.Call(hdc)
	if hdcMem == 0 {
		return false
	}
	defer procDeleteDC.Call(hdcMem)

	bi := bitmapInfo{Header: bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(w),
		Height:   -int32(h), // 负高度 = 自上而下
		Planes:   1,
		BitCount: 32,
	}}
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIB.Call(hdcMem, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	runtime.KeepAlive(&bi)
	if hbm == 0 || bits == nil {
		return false
	}
	defer procDeleteObject.Call(hbm)
	copy(unsafe.Slice((*uint32)(bits), len(px)), px)

	old, _, _ := procSelectObject.Call(hdcMem, hbm)
	// BLENDFUNCTION 是 4 字节、按值传的结构（不是指针！），x64 上打成一个寄存器传进去。
	// 之前传的是 &bf，AlphaBlend 直接失败，按钮整个画不出来。
	bf := uintptr(acSrcOver) |
		uintptr(255)<<16 |
		uintptr(acSrcAlpha)<<24
	ok, _, _ := procAlphaBlend.Call(hdc,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		hdcMem, 0, 0, uintptr(w), uintptr(h),
		bf)
	procSelectObject.Call(hdcMem, old)
	return ok != 0
}

// inRoundRectF 是浮点版的圆角矩形判定，给超采样用。
func inRoundRectF(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx, cy := x, y
	switch {
	case x < x0+r && y < y0+r:
		cx, cy = x0+r, y0+r
	case x > x1-r && y < y0+r:
		cx, cy = x1-r, y0+r
	case x < x0+r && y > y1-r:
		cx, cy = x0+r, y1-r
	case x > x1-r && y > y1-r:
		cx, cy = x1-r, y1-r
	default:
		return true
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// inButton 判断一个客户区坐标是不是落在按钮上。
func inButton(l layout, x, y int) bool {
	return x >= l.btnX() && x < l.btnX()+l.btnW() &&
		y >= l.btnY() && y < l.btnY()+l.btnH()
}

// paintPlaceholder 在结果区为空时画出灰色使用提示。
func paintPlaceholder(hdc uintptr, l layout) {
	if len(app.result.Cards) > 0 || len(app.result.Chips) > 0 || app.result.Note != "" {
		return
	}
	rc := rect{
		Left:   int32(l.resX()),
		Top:    int32(l.resY()),
		Right:  int32(l.resX() + l.resW()),
		Bottom: int32(l.resY() + l.resH()),
	}
	procSetTextColor.Call(hdc, uintptr(th.Hint))
	procSetBkMode.Call(hdc, bkTransparent)
	if app.fontSm != 0 {
		procSelectObject.Call(hdc, app.fontSm)
	}
	txt := utf16(placeholderText)
	if len(txt) == 0 {
		return
	}
	procDrawText.Call(hdc, pptr(&txt[0]), ^uintptr(0), pptr(&rc),
		dtLeft|dtWordBreak|dtNoPrefix)
	runtime.KeepAlive(&txt)
	runtime.KeepAlive(&rc)
}

// applyShape 把窗口裁成圆角。
// 用 SetWindowRgn 而不是只靠 DWM：DWM 的圆角对无边框弹窗不一定生效，
// 而区域裁剪在 Win10/11 上都可靠。
func applyShape(w, h, radius int) {
	rgn, _, _ := procCreateRoundRgn.Call(0, 0, uintptr(w+1), uintptr(h+1),
		uintptr(radius*2), uintptr(radius*2))
	if rgn == 0 {
		return
	}
	// SetWindowRgn 成功后会接管这个区域的所有权，失败才需要自己删
	if r, _, _ := procSetWindowRgn.Call(app.hwnd, rgn, 1); r == 0 {
		procDeleteObject.Call(rgn)
	}
	// Win11 上再让 DWM 圆一次，能拿到抗锯齿的角
	const dwmwaWindowCornerPreference = 33
	const dwmwcpRound = 2
	var pref int32 = dwmwcpRound
	procDwmSetAttribute.Call(app.hwnd, dwmwaWindowCornerPreference,
		uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
	runtime.KeepAlive(&pref)
}

// centerOnMonitor 返回窗口在"光标所在显示器"工作区里居中时的左上角坐标。
func centerOnMonitor(w, h int) (int, int) {
	var p point
	procGetCursor.Call(pptr(&p))
	runtime.KeepAlive(&p)

	// MonitorFromPoint 的 POINT 是按值传的 8 字节结构，x64 上打成一个寄存器传进去
	packed := uintptr(uint32(p.X)) | uintptr(uint32(p.Y))<<32
	hmon, _, _ := procMonitorFromPoint.Call(packed, monitorDefaultToNearest)

	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if hmon != 0 {
		if r, _, _ := procGetMonitorInfo.Call(hmon, pptr(&mi)); r == 0 {
			mi.RcWork = rect{} // 读失败就退回主屏
		}
	}
	runtime.KeepAlive(&mi)

	area := mi.RcWork
	if area.Right <= area.Left || area.Bottom <= area.Top {
		area = rect{
			Right:  int32(one(procGetMetrics, sm_CXScreen)),
			Bottom: int32(one(procGetMetrics, sm_CYScreen)),
		}
	}
	x := int(area.Left) + (int(area.Right-area.Left)-w)/2
	y := int(area.Top) + (int(area.Bottom-area.Top)-h)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// ctlColor 处理 WM_CTLCOLOREDIT / WM_CTLCOLORSTATIC：让输入框与结果框跟窗口同色，
// 去掉系统默认那种突兀的白底/灰底。
func ctlColor(hdc uintptr) uintptr {
	procSetTextColor.Call(hdc, uintptr(th.Text))
	procSetBkColor.Call(hdc, uintptr(th.Bg))
	return th.brEdit
}
