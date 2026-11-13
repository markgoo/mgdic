# mgdic 交接文档

写给接手这个项目的 agent。面向用户的说明在 [README.md](README.md)，这里只写**你接手需要知道、
但从代码里看不出来的东西**：环境事实、不变量、无人值守验证方法、以及踩过的坑。

## 一句话

Windows 本地词典：`Alt+E` 呼出小窗 → 查词/词组/句子 → 复制结果。词与词组离线（ECDICT 76 万词条），
只有整句翻译调 DeepSeek。纯 Go + 手写 Win32，单 exe 11MB，启动 0.2 秒。

## 当前状态

能跑，已发 v0.1.0（<https://github.com/markgoo/mgdic/releases/tag/v0.1.0>）。

**已端到端验证**（用下面「无人值守验证」的方法实测，不是"看代码觉得对"）：

- `Alt+E` → 窗口显示；再按 → 收起；ESC → 收起
- 逐字符输入 → 结果更新（`apple` / `runned`→run / `applicaton`→application / `苹果`→apple）
- 回车翻译整句 → `[DeepSeek] 我非常喜欢游泳`
- 首次运行无库无 CSV → 自动下载 66MB → 建库 168MB → 能查
- 托盘菜单、开机自启勾选状态、版本号显示
- **点圆角框复制**（2026-11-13 实测）：先往剪贴板放哨兵 `SENTINEL-BEFORE-CLICK`，
  发 `WM_LBUTTONUP` 点 `apple` 的卡片，`Get-Clipboard` 读回
  `n. 苹果, 家伙\r\n[医] 苹果`，`startup.log` 同步出现 `已复制：...`。
  上一台机器剪贴板整体坏掉（`OpenClipboard` 返回 `Access is denied`）是那台机器的问题，不是代码问题。

**未验证 —— 需要真人确认**：

- **真实用户的键盘手感**（输入法、长句、快速连打）。自动化只能发 `WM_CHAR`，测不到 IME。

## 环境事实

这台开发机上的具体位置，agent 靠"看"是找不到的：

| 东西 | 位置 |
|---|---|
| Go | `C:\Users\markg\AppData\Local\Programs\Go\go\bin\go.exe`（用户级安装，**不保证在 PATH 里**） |
| 词典库 | `D:\Projects\mgdic\bin\mgdic.db`（168MB，由 CSV 建出来，不入库） |
| 源 CSV | `D:\Projects\mgdic\.vendor\ecdict.csv`（66MB，不入库） |
| 运行日志 | `D:\Projects\mgdic\bin\startup.log`（`config.json` 里 `"log": false` 可关） |
| 构建脚本 | `build.ps1`（两个 exe）、`release.ps1`（发版） |

`bin/` 和 `.vendor/` 都在 `.gitignore` 里。**仓库里没有数据文件，这是有意的**——ECDICT 的版权不在我们这里。

## 代码地图

```
cmd/mgdic/        入口 + 首次运行下载（download.go）
internal/lexicon/ 纯数据层：读 CSV、算归一化键、抽中文词串（不碰数据库）
internal/store/   SQLite：建库、CSV 指纹、四种查询
internal/query/   查询逻辑：方向判断、降级顺序、卡片/词块生成（含单测）
internal/winui/   Win32：热键、托盘、自绘圆角框、RichEdit 结果框
internal/config/  配置
internal/llm/     chat/completions 客户端（只有整句翻译用）
internal/version/ 版本号（发版只改这里）
```

分层的关键：`lexicon` 不知道数据库存在，`query` 不知道 Windows 存在，`winui` 不知道词典存在
（它只接收 `Result{Cards, Chips, Note}`）。改动时守住这条线，测试才好写。

## 三个不变量

**1. 打字不联网。** 只有回车和「AI 翻译」按钮会调 API。改查询逻辑时，
`Answer.NeedsLLM` 决定"回车有没有用"，别让它对命中词条的输入变成 true——那会让用户打字时偷偷花钱。

**2. 数据不进仓库。** `ecdict.csv` / `*.db` / `bin/` 都在 `.gitignore` 里。
新增任何数据文件前先想清楚版权归谁。

**3. 结果区分两层。** 圆角框放"能直接复制走的主信息"（自己画的，点击复制），
下方 RichEdit 放辅助信息（可拖选复制）。两者由 `Result.Cards[].Lines` 和 `.Title/.Meta` 分开驱动。

## 无人值守验证（重要）

这个程序是 GUI，但你**可以完全不看屏幕地验证它**。做法是给窗口发消息：

```powershell
# 找到窗口：这台机器上 FindWindowW("mgdic.Popup") 会返回 0（原因未查明），
# 用 EnumWindows + GetClassNameW 按类名找才靠得住
# 显示/收起（等价于按 Alt+E）
SendMessage($hwnd, 0x0312, 1, 0)              # WM_HOTKEY
# 往输入框打字
SendMessage($input, 0x0102, [int][char]'a', 0) # WM_CHAR
# 点按钮（客户区坐标）
SendMessage($hwnd, 0x0201, 1, ($y -shl 16) -bor $x)  # WM_LBUTTONDOWN
SendMessage($hwnd, 0x0202, 0, ($y -shl 16) -bor $x)  # WM_LBUTTONUP
```

**看结果**：`SendMessage($richedit, 0x000D, bufSize, $sb)`（`WM_GETTEXT`）把文字读回来。
剪贴板不可靠时，用 `startup.log` 里的 `已复制：...` 确认点了什么。

**截图**：`PrintWindow(hwnd, hdc, 2)` 只渲染这个窗口，不受遮挡影响；
但它走 GDI 打印路径、**不渲染 ClearType**，判断字体效果要用 `CopyFromScreen` 截真实屏幕。
读窗口样式用 `GetWindowLong(hwnd, -16)`——`WS_DISABLED` 置位就意味着窗口收不到键盘。

**诊断启动失败**：GUI 版没有控制台，panic 完全看不见。用控制台版：

```powershell
.\bin\mgdic-cli.exe --selfcheck     # 打印每一步 Win32 调用的成败
```

它会先跑 `checkProcs()` 把所有用到的导出解析一遍，名字错了会变成一句能读懂的报错而不是崩溃。

## 发版

```powershell
.\release.ps1 -Version 0.2.0     # 改号 → 编译 → vet/test → 提交 → tag → 推送 → 建 Release → 传资产
.\release.ps1 -DryRun            # 只做本地部分，验证脚本
```

版本号只在 `internal/version/version.go` 一处，脚本代劳。

凭据优先用 `$env:GH_TOKEN`，否则读 Git 凭据管理器；**脚本从不打印 token**。
（上一任 agent 曾把 token 打进对话，别再犯。）

## 下一步

按价值排序：

1. ~~确认剪贴板~~ —— 已验证通过，见上面「已端到端验证」。
2. **中文方向的候选质量**。倒排索引有固有噪音：`喜欢 → care for`（ECDICT 的 `like` 词条里没有
   "喜欢"这个释义）、`美丽 → pulchritude`（`beautiful` 写的是"美丽的"）。
   `query.zhCandidateScore` 是调权重的入口，改完用 `mgdic-cli.exe q <词>` 批量对比。
3. **接第二门语言**。日语走 EDRDG 的 JMdict（CC BY-SA 4.0），韩语走 krdict（自带中文释义）。
   来源已核实，见 [docs/multilingual-dict-sources.md](docs/multilingual-dict-sources.md)。
   数据层要加一张 `xx2en` 表，`store.ZhCandidates` 是个可参照的形状。
4. 窗口圆角目前由 `SetWindowRgn` 裁出来，没有抗锯齿。要平滑得改成分层窗口
   （`WS_EX_LAYERED` + `UpdateLayeredWindow`），改动较大。

## 参考

- **[docs/win32-pitfalls.md](docs/win32-pitfalls.md)** —— 这个项目踩过的一批 Win32 **静默失败**：
  常量写错一位、结构体少字段、`RegisterClassExW` 在这台机器上不可用、双缓冲顺序反了等等。
  **动手改 `internal/winui` 之前先读它**，那些坑全部不报错、只能靠看。
- [docs/multilingual-dict-sources.md](docs/multilingual-dict-sources.md) —— 多语言词典来源与许可核实。
