# Win32 静默失败的坑

这个项目用 Go 直接调 Win32（没有用现成的 GUI 库），踩了一批**不报错、只能靠看**的坑。
记在这里，免得下次重写再踩一遍。

## 常量写错一位

| 写错 | 正确 | 症状 |
|---|---|---|
| `WM_HOTKEY = 0x0303` | `0x0312` | 热键注册成功、Windows 也发了消息，但窗口过程认不出来 → **按 Alt+E 毫无反应** |
| `WS_POPUP = 0x08000000` | `0x80000000` | `0x08000000` 其实是 `WS_DISABLED` → 窗口带标题栏、且**收不到键盘输入**（打字没反应） |
| `WM_SETFONT = 0x0031` | `0x0030` | `0x0031` 是 `WM_GETFONT` → `setFont()` 一直在"获取字体"，**所有控件用的都是系统默认字体** |
| `EM_SETSEL = 0x00C1` | `0x00B1` | 全选失效 |
| `ES_AUTOVSCROLL = 0x0100` | `0x0040` | `0x0100` 是 `ES_AUTOHSCROLL` → 结果框横向滚而不是纵向 |
| `WS_TABSTOP = 0x00000010` | `0x00010000` | 控件不在 Tab 顺序里 |

`internal/winui/consts_test.go` 现在把 50 多个常量按文档值钉住。

> **但第一次写这个测试时，我把 `WS_POPUP` 的期望值也写成了错的**——等于用一个错值验证另一个错值，
> 测试全绿但程序不能用。常量的可信来源只能是文档，不是记忆。

## 结构体少字段

`LOGFONTW` 漏了 `lfWeight` / `lfItalic` / `lfUnderline` / `lfStrikeOut` 四个字段，结构体短 4 字节。
后果连锁：

- `lfWeight` 被读成 1（极细字重）
- `lfQuality` 读到字体名里的一个字节（非法值 → 退回 `DEFAULT_QUALITY`，ClearType 失效）
- `lfFaceName` 从错位偏移读成 `"crosoft YaHei UI"`（找不到字体，退回系统默认）

验证方式不是看截图（`PrintWindow` 走 GDI 打印路径，不渲染 ClearType），而是
`WM_GETFONT` + `GetObjectW` 把控件实际用的 `LOGFONT` 读回来。

## 这台机器上 `RegisterClassExW` 不可用

对任何 `cbSize`（72 / 76 / 80 / 84 / 88）都返回 `ERROR_INVALID_PARAMETER`，
而同样数据的 **`RegisterClassW` 稳定成功**。结构体字节逐字段核对过是对的，不是布局问题。

改用老 API：`WNDCLASSW` 没有 `cbSize` 和 `hIconSm`，无边框弹窗本来也用不到。
定位过程：`GetSystemInfo`、`GetCursorPos`、`CreateWindowEx("BUTTON")` 都能成功，
说明窗口站、桌面、指针递交机制都正常，问题精确地出在 `RegisterClassExW` 这一次调用上。

## 导出名写错会 panic，不是返回错误

`x/sys` 的 `LazyProc` 在导出名不存在时**直接 panic**。把 `DispatchMessageW` 写成 `DispatchMessage`，
程序一路跑到消息循环才崩，用户看到的就是"闪退"。

现在启动时用 `checkProcs()` 把所有导出解析一遍，名字错了会变成一句能读懂的报错。

## 双缓冲的两个顺序问题

- `BitBlt` 必须在**取消选中位图之前**，否则读的是源 DC 里默认那张 1×1 位图 → 窗口直接变透明
- 贴图前要**清掉 `BeginPaint` 设的裁剪区**，否则只重绘了无效区域，其余像素还是上一帧的

## 其他

- **`EDIT` 控件只认 `\r\n`**：给 `\n` 会被渲染成空格，整段挤成一行
- **`EM_SETBKGNDCOLOR` 发给 RichEdit 会直接访问违例崩溃**（`0xc0000005`）。
  GUI 版没有控制台完全看不见，是靠 `mgdic-cli.exe --selfcheck` 打出 panic 栈定位的
- **`DrawText` 默认不透明背景模式**：会先用背景色刷一遍文字区，白字盖白底 → 看起来"按钮上没有字"。
  要 `SetBkMode(TRANSPARENT)`
- **`AlphaBlend` 的 `BLENDFUNCTION` 是按值传的 4 字节结构**，传指针会直接失败
- **`EM_SETCUEBANNER` 的提示在控件拿到焦点时消失**，而输入框一直是焦点状态 → 自己画占位提示
- **`GetThreadDesktop` 要的是线程 ID**，不是 `GetCurrentThread` 的伪句柄 `-2`。传错会永远返回 0，
  于是每条线程都被误判成"没有桌面"
- **同一段文字没被重绘**：换成"自绘框 + RichEdit 文本框"后，文本框自己会更新，但自绘部分需要
  `InvalidateRect` 才会重画——漏了就会看到框里还是上一个词的内容
