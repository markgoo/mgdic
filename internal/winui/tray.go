package winui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 托盘图标：Shell_NotifyIconW + 一个右键菜单。
//
// 这是这个程序唯一"常驻可见"的东西：窗口平时是隐藏的，靠托盘图标告诉用户它还活着，
// 也提供唯一的正常退出入口（在此之前只能去任务管理器杀进程）。

const (
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4

	nifMessage = 0x0001
	nifIcon    = 0x0002
	nifTip     = 0x0004
	nifInfo    = 0x0010
	nifShowTip = 0x0080

	niifInfo = 0x00000001

	notifyIconVersion4 = 4

	// NOTIFYICON_VERSION_4 下，回调消息的低字是这些通知码
	ninSelect           = 0x0400
	ninKeySelect        = 0x0401
	ninBalloonUserClick = 0x0405

	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmNull          = 0x0000

	mfString       = 0x0000
	mfSeparator    = 0x0800
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	idiApplication = 32512

	// 菜单项编号；用 TPM_RETURNCMD 让 TrackPopupMenu 直接返回它，省掉 WM_COMMAND 分支
	cmdOpen       = 1001
	cmdOpenConfig = 1002
	cmdQuit       = 1003
)

// notifyIconData 是 NOTIFYICONDATAW（x64，sizeof = 976）。
// 字段顺序与填充按 C 的布局排，cbSize 必须精确对上，否则 Shell_NotifyIcon 直接失败。
type notifyIconData struct {
	Size            uint32      // 0
	_               uint32      // 4  对齐 hWnd
	Hwnd            uintptr     // 8
	ID              uint32      // 16
	Flags           uint32      // 20
	CallbackMessage uint32      // 24
	_               uint32      // 28 对齐 hIcon
	Icon            uintptr     // 32
	Tip             [128]uint16 // 40  .. 296
	State           uint32      // 296
	StateMask       uint32      // 300
	Info            [256]uint16 // 304 .. 816
	Version         uint32      // 816
	InfoTitle       [64]uint16  // 820 .. 948
	InfoFlags       uint32      // 948
	GuidItem        [16]byte    // 952 .. 968
	BalloonIcon     uintptr     // 968 .. 976
}

// bitmapInfoHeader 是 BITMAPINFOHEADER。
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// iconInfo 是 ICONINFO（x64，sizeof = 32）。
type iconInfo struct {
	FIcon    int32   // 0
	XHotspot uint32  // 4
	YHotspot uint32  // 8
	_        uint32  // 12 对齐 hbmMask
	HbmMask  uintptr // 16
	HbmColor uintptr // 24
}

var (
	shell32 = windows.NewLazyDLL("shell32.dll")

	procNotifyIcon    = shell32.NewProc("Shell_NotifyIconW")
	procShellExecute  = shell32.NewProc("ShellExecuteW")
	procLoadIcon      = user32.NewProc("LoadIconW")
	procCreatePopup   = user32.NewProc("CreatePopupMenu")
	procAppendMenu    = user32.NewProc("AppendMenuW")
	procTrackPopup    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu   = user32.NewProc("DestroyMenu")
	procGetConsole    = kernel32.NewProc("GetConsoleWindow")
	procConsoleProcs  = kernel32.NewProc("GetConsoleProcessList")
	procRegisterMsg   = user32.NewProc("RegisterWindowMessageW")
	procCreateDIB     = gdi32.NewProc("CreateDIBSection")
	procCreateIconInd = user32.NewProc("CreateIconIndirect")
	procMessageBox    = user32.NewProc("MessageBoxW")
	procCreateBitmap  = gdi32.NewProc("CreateBitmap")
	procDeleteObject  = gdi32.NewProc("DeleteObject")
	procCreateDC      = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC      = gdi32.NewProc("DeleteDC")
)

// ---- 开机自启 ----
//
// 写的是当前用户的 HKCU\Software\Microsoft\Windows\CurrentVersion\Run，
// 不需要管理员权限，也不会污染全机。值是带引号的 exe 绝对路径（路径里有空格也能用）。

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "mgdic"
)

// autoStartEnabled 判断注册表里有没有我们这条启动项。
func autoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValueName)
	if err != nil {
		return false
	}
	return strings.TrimSpace(v) != ""
}

// setAutoStart 打开或关闭开机自启。
func setAutoStart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValueName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValueName, `"`+exe+`"`)
}

// trayState 是托盘需要的运行时状态。
type trayState struct {
	icon       uintptr
	taskbar    uint32 // "TaskbarCreated" 的注册消息号：Explorer 重启后靠它把图标挂回去
	configPath string
	version    string
}

var tray trayState

// tipText 是托盘图标的提示文字，带上版本号。
func tipText() string {
	if tray.version == "" {
		return "mgdic 词典 · Alt+E 呼出"
	}
	return "mgdic v" + tray.version + " · Alt+E 呼出"
}

// initTray 建图标、挂到托盘上，并注册"任务栏重建"消息。
func initTray(configPath, ver string) error {
	tray.configPath = configPath
	tray.version = ver
	tray.icon = makeTrayIcon()

	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:            app.hwnd,
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip | nifShowTip,
		CallbackMessage: wmTray,
		Icon:            tray.icon,
	}
	copyTip(&nid.Tip, tipText())
	if err := shellNotify(nimAdd, &nid); err != nil {
		return err
	}
	// 升到 v4：回调的低字才会给出 NIN_SELECT 这类通知码，而不是原始的鼠标消息
	v4 := nid
	v4.Version = notifyIconVersion4
	_ = shellNotify(nimSetVersion, &v4)

	if name, err := windows.UTF16PtrFromString("TaskbarCreated"); err == nil {
		if r, _, _ := procRegisterMsg.Call(uintptr(unsafe.Pointer(name))); r != 0 {
			tray.taskbar = uint32(r)
		}
	}
	return nil
}

func removeTray() {
	if app.hwnd == 0 {
		return
	}
	nid := notifyIconData{
		Size: uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd: app.hwnd,
		ID:   1,
	}
	_ = shellNotify(nimDelete, &nid)
}

func shellNotify(msg uint32, nid *notifyIconData) error {
	r, _, e := procNotifyIcon.Call(uintptr(msg), uintptr(unsafe.Pointer(nid)))
	runtime.KeepAlive(nid)
	if r == 0 {
		return e
	}
	return nil
}

// readdTray 是 Explorer 重启后把图标挂回去：不处理的话托盘图标会凭空消失。
func readdTray() {
	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:            app.hwnd,
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip | nifShowTip,
		CallbackMessage: wmTray,
		Icon:            tray.icon,
	}
	copyTip(&nid.Tip, tipText())
	_ = shellNotify(nimAdd, &nid)
}

// showBalloon 从托盘弹一个提示气泡。
// 存在的理由很实际：控制台被藏起来之后，用户双击完看不到任何反应，
// 会以为程序没起来（"托盘在哪呢"）。气泡就是那句"我在这儿，按 Alt+E"。
func showBalloon(title, text string) {
	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:            app.hwnd,
		ID:              1,
		Flags:           nifInfo,
		CallbackMessage: wmTray,
	}
	copyTip(&nid.Tip, tipText())
	copyFixed(nid.InfoTitle[:], title)
	copyFixed(nid.Info[:], text)
	nid.InfoFlags = niifInfo
	_ = shellNotify(nimModify, &nid)
}

// copyFixed 把字符串拷进定长 UTF-16 数组，留一个 NUL。
func copyFixed(dst []uint16, s string) {
	u := utf16(s)
	for i := 0; i < len(u) && i < len(dst)-1; i++ {
		dst[i] = u[i]
	}
}

// copyTip 把字符串拷进提示文字。
func copyTip(dst *[128]uint16, s string) {
	copyFixed(dst[:], s)
}

// trayCallback 处理托盘图标上的点击。
// v4 的 lParam：低字是通知码，高字是图标 id。
func trayCallback(lparam uintptr) {
	code := lparam & 0xFFFF
	switch code {
	case ninSelect, ninKeySelect, wmLButtonDblClk, wmRButtonUp, wmContextMenu:
		// 左键单击/双击都直接呼出词典；右键出菜单
		if code == wmRButtonUp || code == wmContextMenu {
			showTrayMenu()
			return
		}
		showAtCursor()
	case ninBalloonUserClick:
		showAtCursor()
	}
}

// showTrayMenu 在光标处弹右键菜单。用 TPM_RETURNCMD 直接拿选择结果，不必再处理 WM_COMMAND。
func showTrayMenu() {
	menu, _, _ := procCreatePopup.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	addMenuItem(menu, cmdOpen, "打开词典 (Alt+E)")
	addMenuItem(menu, cmdOpenConfig, "打开配置文件")
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	// 开机自启：带勾选状态
	flags := uintptr(mfString)
	if autoStartEnabled() {
		flags |= mfChecked
	}
	labelAuto := utf16("开机自启")
	procAppendMenu.Call(menu, flags, uintptr(cmdAutoStart), pptr(&labelAuto[0]))
	runtime.KeepAlive(&labelAuto)
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	if tray.version != "" {
		label := utf16("mgdic  v" + tray.version)
		// 只显示不可点，所以带 MF_GRAYED
		procAppendMenu.Call(menu, mfString|mfGrayed, 0, pptr(&label[0]))
		runtime.KeepAlive(&label)
	}
	addMenuItem(menu, cmdQuit, "退出")

	var p point
	procGetCursor.Call(uintptr(unsafe.Pointer(&p)))

	// 弹菜单前要先把窗口提到前台，弹完再补一个空消息，
	// 否则点菜单外面菜单不会消失（这是 TrackPopupMenu 的老规矩）
	procForeground.Call(app.hwnd)
	cmd, _, _ := procTrackPopup.Call(menu,
		tpmRightButton|tpmReturnCmd,
		uintptr(p.X), uintptr(p.Y), 0, app.hwnd, 0)
	procPost.Call(app.hwnd, wmNull, 0, 0)

	switch cmd {
	case cmdOpen:
		showAtCursor()
	case cmdOpenConfig:
		openInEditor(tray.configPath)
	case cmdAutoStart:
		on := !autoStartEnabled()
		if err := setAutoStart(on); err != nil {
			logLine("开机自启设置失败: " + err.Error())
			ShowError("mgdic", "设置开机自启失败："+err.Error())
		} else {
			logLine(fmt.Sprintf("开机自启 -> %v", on))
		}
	case cmdQuit:
		procPost.Call(app.hwnd, wmClose, 0, 0)
	}
}

func addMenuItem(menu uintptr, id int, text string) {
	label := utf16(text)
	procAppendMenu.Call(menu, mfString, uintptr(id), pptr(&label[0]))
	runtime.KeepAlive(&label)
}

// openInEditor 用系统默认程序打开配置文件（.json 一般落在编辑器里）。
func openInEditor(path string) {
	if path == "" {
		return
	}
	op := utf16("open")
	file := utf16(path)
	procShellExecute.Call(0, pptr(&op[0]), pptr(&file[0]), 0, 0, swShowNormal)
	runtime.KeepAlive(&op)
	runtime.KeepAlive(&file)
}

// hideConsole 把控制台窗口藏起来，但只在我们独占它的时候（双击 exe 的情形）。
// 从 PowerShell/终端里启动时，GetConsoleWindow 返回的是用户自己的终端窗口，
// 藏掉等于把人家正在用的窗口弄没了，所以要先数一下挂在这个控制台上的进程有几个。
func hideConsole() {
	h, _, _ := procGetConsole.Call()
	if h == 0 {
		return
	}
	var pids [4]uint32
	n, _, _ := procConsoleProcs.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	runtime.KeepAlive(&pids)
	if n > 1 {
		return // 和别人共用控制台（终端里启动的），不动它
	}
	procShow.Call(h, swHide)
}

// ShowError 弹一个系统错误对话框。
// 双击启动时控制台会随进程一起消失，启动失败就没有任何痕迹，所以失败必须弹窗说出来。
func ShowError(title, text string) {
	t := utf16(title)
	m := utf16(text)
	if len(t) == 0 || len(m) == 0 {
		return
	}
	const mbIconError = 0x00000010
	procMessageBox.Call(0, pptr(&m[0]), pptr(&t[0]), mbIconError)
	runtime.KeepAlive(&t)
	runtime.KeepAlive(&m)
}

// makeTrayIcon 画一个 32×32 的托盘图标：蓝底圆角方块 + 白色"页面" + 三道短线。
// 画不出来就退回系统自带的应用图标，绝不因为图标失败而让托盘挂不上。
func makeTrayIcon() uintptr {
	if h := drawIcon(32); h != 0 {
		return h
	}
	h, _, _ := procLoadIcon.Call(0, uintptr(idiApplication))
	return h
}

// drawIcon 在 Go 里直接算像素（BGRA，自上而下），再交给 CreateDIBSection 与 CreateIconIndirect。
// 比在 GDI 里画字稳：没有字体、没有抗锯齿依赖，尺寸也就不会随 DPI 变。
func drawIcon(size int) uintptr {
	px := make([]uint32, size*size)
	const (
		bgColor   = 0xFFEB6F2F // #2F6FEB，注意是 BGRA
		pageColor = 0xFFFFFFFF
		lineColor = 0xFFEB6F2F
	)
	// 底：圆角方块
	fillRoundRect(px, size, 0, 0, size-1, size-1, 6, bgColor)
	// 白纸
	fillRoundRect(px, size, 7, 6, size-8, size-7, 2, pageColor)
	// 三道字行
	line := func(y, x0, x1 int) {
		for yy := y; yy < y+2; yy++ {
			for xx := x0; xx <= x1; xx++ {
				if xx >= 0 && xx < size && yy >= 0 && yy < size {
					px[yy*size+xx] = lineColor
				}
			}
		}
	}
	line(11, 11, size-12)
	line(15, 11, size-12)
	line(19, 11, size-16)

	hdc, _, _ := procCreateDC.Call(0)
	defer procDeleteDC.Call(hdc)

	bi := bitmapInfo{Header: bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(size),
		Height:   -int32(size), // 负高度 = 自上而下，和我们的像素顺序一致
		Planes:   1,
		BitCount: 32,
	}}
	// ppvBits 是 LPVOID*，所以这里用 unsafe.Pointer 直接接住它，
	// 不要先落到 uintptr 再转回来——那种写法既不安全，vet 也会拦。
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIB.Call(hdc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	runtime.KeepAlive(&bi)
	if hbm == 0 || bits == nil {
		return 0
	}
	dst := unsafe.Slice((*uint32)(bits), len(px))
	copy(dst, px)

	// 掩码位图：1bpp，每行补齐到 2 字节；全 0 表示"用颜色位图"，透明度交给 alpha 通道
	maskRow := ((size + 15) / 16) * 2
	maskBits := make([]byte, maskRow*size)
	hmask, _, _ := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1,
		uintptr(unsafe.Pointer(&maskBits[0])))
	runtime.KeepAlive(&maskBits)
	if hmask == 0 {
		procDeleteObject.Call(hbm)
		return 0
	}

	ii := iconInfo{FIcon: 1, HbmMask: hmask, HbmColor: hbm}
	icon, _, _ := procCreateIconInd.Call(uintptr(unsafe.Pointer(&ii)))
	runtime.KeepAlive(&ii)

	// CreateIconIndirect 会把位图复制走，原件可以立刻释放
	procDeleteObject.Call(hbm)
	procDeleteObject.Call(hmask)
	return icon
}

// fillRoundRect 往像素缓冲里填一个圆角矩形，坐标闭区间。
func fillRoundRect(px []uint32, size, x0, y0, x1, y1, r int, color uint32) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if x < 0 || x >= size || y < 0 || y >= size {
				continue
			}
			if inRoundRect(x, y, x0, y0, x1, y1, r) {
				px[y*size+x] = color
			}
		}
	}
}

// inRoundRect 判断点是否落在圆角矩形内：四个角用圆判，其余按矩形判。
func inRoundRect(x, y, x0, y0, x1, y1, r int) bool {
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
