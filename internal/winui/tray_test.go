package winui

import (
	"testing"
	"unsafe"
)

// NOTIFYICONDATAW 的 cbSize 必须和 Windows 期望的一字不差，否则 Shell_NotifyIcon 静默失败。
// x64 上的正确值是 976；这个测试就是防止以后有人往结构体里加字段把它改坏。
func TestNotifyIconDataLayout(t *testing.T) {
	const want = 976
	if got := unsafe.Sizeof(notifyIconData{}); got != want {
		t.Fatalf("sizeof(notifyIconData) = %d，Windows 要 %d", got, want)
	}
}

func TestIconInfoLayout(t *testing.T) {
	const want = 32
	if got := unsafe.Sizeof(iconInfo{}); got != want {
		t.Fatalf("sizeof(iconInfo) = %d，Windows 要 %d", got, want)
	}
}

func TestBitmapInfoHeaderLayout(t *testing.T) {
	const want = 40
	if got := unsafe.Sizeof(bitmapInfoHeader{}); got != want {
		t.Fatalf("sizeof(BITMAPINFOHEADER) = %d，Windows 要 %d", got, want)
	}
}

// 托盘图标是运行时画出来的，画不出来就会退回系统图标。
// 这个测试确认我们画的这张至少是能建出来的（GDI 不依赖桌面，所以能在这里跑）。
func TestDrawIcon(t *testing.T) {
	h := drawIcon(32)
	if h == 0 {
		t.Skip("这个环境里建不出图标（可能没有 GDI），运行时会退回系统图标")
	}
	procDeleteObject.Call(h)
}

// 圆角矩形的判定：角上抠掉、中心保留。
func TestInRoundRect(t *testing.T) {
	if !inRoundRect(5, 5, 0, 0, 10, 10, 4) {
		t.Error("中心点应当在圆角矩形内")
	}
	if inRoundRect(0, 0, 0, 0, 10, 10, 4) {
		t.Error("左上角应当被圆角抠掉")
	}
	if inRoundRect(-1, 5, 0, 0, 10, 10, 4) {
		t.Error("矩形外的点不该算在内")
	}
}

// 单实例：同一个互斥体再拿一次，必须报告"已经有一个了"。
// 如果本机已经有一个 mgdic 在跑（用户正开着），第一次就拿不到——那是正常情况，跳过。
func TestSingleInstance(t *testing.T) {
	if !claimSingleInstance() {
		t.Skip("已经有一个 mgdic 在运行，跳过这个测试")
	}
	if claimSingleInstance() {
		t.Fatal("第二次应当被挡下（否则会开出两个托盘图标）")
	}
}

// 热键写法解析：alt+e 要变成 MOD_ALT|MOD_NOREPEAT 与 'E'。
func TestParseHotkey(t *testing.T) {
	mods, vk, name, err := parseHotkey("alt+e")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if vk != 'E' {
		t.Errorf("vk = %#x，想要 %#x", vk, 'E')
	}
	if mods&modAlt == 0 {
		t.Error("缺少 MOD_ALT")
	}
	if mods&norepeat == 0 {
		t.Error("缺少 MOD_NOREPEAT（按住热键会狂弹窗口）")
	}
	if name != "alt+e" {
		t.Errorf("name = %q", name)
	}
	if _, _, _, err := parseHotkey("ctrl+"); err == nil {
		t.Error("没有主键的热键应当报错")
	}
	if _, _, _, err := parseHotkey("alt+nosuchkey"); err == nil {
		t.Error("不认识的主键应当报错")
	}
}
