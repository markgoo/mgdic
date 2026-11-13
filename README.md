# mgdic

一个只做一件事的本地词典：**Alt+E 呼出小窗 → 输入词/词组/句子 → 看结果 → 复制走**。

词与词组的翻译**完全离线**（76 万词条），只有整句翻译才调用 DeepSeek。
比有道词典轻一个数量级：exe 11MB、常驻内存 20MB、启动 0.2 秒。

![查英文](screenshot/02-english-word.png)

## 快速开始

### 1. 自己下载词典数据

**本项目不随仓库分发任何词典数据**——版权不在我这里，仓库里也不该出现几十上百 MB 的数据文件。
词典数据请从 **[ECDICT](https://github.com/skywind3000/ECDICT)** 官方下载（MIT 许可，版权归其作者）：

```powershell
curl.exe -sSL -o ecdict.csv https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv
```

也可以去 [ECDICT Releases](https://github.com/skywind3000/ECDICT/releases) 挑别的格式
（`ecdict-sqlite-28.zip` / `ecdict-stardict-28.zip` / `ecdict-mdx-28.zip`，给 GoldenDict 之类用的）。

> 建出来的 `mgdic.db`（168MB）同样不要提交，`.gitignore` 里已经排除了
> `ecdict.csv` / `*.db` / `bin/` / `.vendor/`。

### 2. 编译

需要 Go 1.21+：

```powershell
.\build.ps1
```

产出两个 exe：

| 文件 | 用途 |
|---|---|
| `bin\mgdic.exe` | GUI 子系统，双击没有控制台窗口，只出托盘图标 |
| `bin\mgdic-cli.exe` | 控制台版，给 `q` / `import` / `config` 用 |

### 3. 首次运行（自动建库）

把 `ecdict.csv` 和 `mgdic.exe` 放同一个文件夹，然后双击 `mgdic.exe`。

**程序会自动把 CSV 建成 SQLite 词典库**（约 10 秒，只做一次，产出 168MB 的 `mgdic.db`）。
之后每次启动只打开这个库，0.2 秒就绪。

> 也可以手动建：`mgdic-cli.exe import`

## 用法

| 操作 | 作用 |
|---|---|
| `Alt+E` | 呼出 / 收起（开关） |
| `Enter` | 翻译整句（只在"需要翻译"时） |
| `Esc` | 收起 |
| `Ctrl+A` / `Ctrl+C` / `Ctrl+V` / `Ctrl+X` | 全选 / 复制 / 粘贴 / 剪切 |
| 右键输入框 | 剪切 / 复制 / 粘贴 / 全选 |
| 点圆角框 | 复制框里那块翻译 |
| 点「AI 翻译」 | 无条件翻译当前输入（单词也行） |
| 托盘左键 / 右键 | 呼出词典 / 菜单（配置、开机自启、版本、退出） |

## 界面

结果区分两层：**圆角框**放能直接复制走的主信息，**只读文本框**放辅助信息（可以拖选其中一段再 Ctrl+C）。

| 输入 | 圆角框（点一下复制） | 文本框（可拖选） |
|---|---|---|
| `apple` | 中文释义 | 词头+音标、考纲标签、词形变化 |
| `苹果` | `apple` | 音标、中文释义、考纲、词形 |
| `I like swimming very much` | — | 每个词一行 + DeepSeek 译文 |

| 查中文 | 查句子 |
|---|---|
| ![查中文](screenshot/03-chinese-word.png) | ![查句子](screenshot/04-sentence.png) |

## 什么时候会调 API

**只有按回车或点「AI 翻译」才会调**，打字过程中一次都不调。

需要翻译的判定：

- 英文 ≥3 个词（或 ≥20 字符）且没命中词条
- 英文单词查不到、拼写纠错也救不回来
- 中文 ≥5 个字，或带句读标点

命中词条、词组、词形还原、拼写纠错、前缀提示 —— 全部离线，不花钱。

![翻译结果](screenshot/05-translated.png)

## 配置

`config.json`（和 exe 同目录，首次运行自动生成）：

| 键 | 默认 | 说明 |
|---|---|---|
| `api_key` | 空 | 填了才能翻整句；也认环境变量 `DEEPSEEK_API_KEY` |
| `base_url` | `https://api.deepseek.com/v1` | OpenAI 兼容接口都吃 |
| `model` | `deepseek-chat` | |
| `hotkey` | `alt+e` | 支持 `ctrl`/`shift`/`win` 组合与 F1–F24 |
| `timeout_ms` | `8000` | 整句翻译超时 |
| `dict_csv` | exe 同级 `ecdict.csv` | 建库的源，只在 import 时读 |
| `dict_db` | exe 同级 `mgdic.db` | 词典库 |
| `log` | `true` | 是否写启动日志；`false` 就完全不写文件 |

**绿色版**：配置、词典库、日志全在 exe 同目录，整个文件夹拷到别的电脑直接能用，不写 `%APPDATA%`。

> ⚠️ `config.json` 里有 API key，别把整个文件夹公开分享。

## 数据

**本项目不打包、不分发词典数据。** 仓库只包含程序源码；数据由使用者自行从上游获取，
版权与许可归上游作者。程序本身只负责把 CSV 转成 SQLite 索引，不改变、不再分发原始数据。

| | |
|---|---|
| 数据来源 | [ECDICT](https://github.com/skywind3000/ECDICT) |
| 许可 | MIT（版权归 ECDICT 作者） |
| 下载 | `https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv`（66MB） |
| 内容 | 76 万词条：音标、中文释义、英文释义、词性、考纲标签（中考/高考/CET4-6/考研/GRE）、柯林斯星级、牛津 3000、传统与现代语料词频、词形变化 |

建库后的 SQLite 结构（**这个库是本地派生产物，也不进仓库**）：

```sql
entries(id, word, key_norm, phonetic, definition, translation, pos, tag, exchange, freq, oxford, collins)
zh2en(tok, entry_id, freq)   -- 中文词串 -> 命中它的英文词条（中文方向靠它）
meta(key, value)             -- schema 版本 + 源 CSV 指纹，判断要不要重建
```

## 查询的降级顺序

**英文**：精确命中 → 词形还原（`runned`→`run`）→ 去连字符 → **拼写纠错**（`applicaton`→`application`，
按编辑距离 1 展开候选，一条 `IN` 查询问完）→ 当句子逐词 → 前缀提示。

**中文**：贪心切词（最长优先）→ 每段只给**最可能的 1 个**英文词（候选噪音很大：
查"苹果"会带出法语词 `pomme`、缩写 `app.`）。

## 踩过的坑

这个项目踩了不少 **Win32 静默失败**的坑——不报错，只能靠看。全记在下面，
`internal/winui` 里对应位置也有注释。

### 常量写错一位

| 写错 | 正确 | 症状 |
|---|---|---|
| `WM_HOTKEY = 0x0303` | `0x0312` | 热键注册成功、Windows 也发了消息，但窗口过程认不出来 → **按 Alt+E 毫无反应** |
| `WS_POPUP = 0x08000000` | `0x80000000` | `0x08000000` 是 `WS_DISABLED` → 窗口带标题栏、且**收不到键盘输入**（打字没反应） |
| `WM_SETFONT = 0x0031` | `0x0030` | `0x0031` 是 `WM_GETFONT` → `setFont()` 一直在"获取字体"，**所有控件用的都是系统默认字体** |
| `EM_SETSEL = 0x00C1` | `0x00B1` | 全选失效 |

`consts_test.go` 现在把 50 多个常量按文档值钉住。
**但第一次写这个测试时，我把 `WS_POPUP` 的期望值也写成了错的**——等于用一个错值验证另一个错值。
常量的可信来源只能是文档，不是记忆。

### 结构体少字段

`LOGFONTW` 漏了 `lfWeight`/`lfItalic`/`lfUnderline`/`lfStrikeOut` 四个字段，结构体短 4 字节。
后果：`lfWeight` 读成 1（极细）、`lfQuality` 读到字体名里的一个字节（非法值 → ClearType 失效）、
`lfFaceName` 从错位偏移读成 `"crosoft YaHei UI"`。

### 这台机器上 `RegisterClassExW` 不可用

对任何 `cbSize`（72/76/80/84/88）都返回 `ERROR_INVALID_PARAMETER`，而同样数据的
**`RegisterClassW` 稳定成功**。结构体字节逐字段核对过是对的，不是布局问题。
改用老 API：`WNDCLASSW` 没有 `cbSize` 和 `hIconSm`，无边框弹窗本来也用不到。

### 导出名写错会 panic，不是返回错误

`x/sys` 的 `LazyProc` 在导出名不存在时直接 panic。把 `DispatchMessageW` 写成 `DispatchMessage`，
程序一路跑到消息循环才崩，用户看到的就是"闪退"。
现在启动时用 `checkProcs()` 把所有导出解析一遍，名字错了会变成一句能读懂的报错。

### 双缓冲的两个顺序问题

- `BitBlt` 必须在**取消选中位图之前**，否则读的是源 DC 里默认那张 1×1 位图 → 窗口变透明
- 贴图前要**清掉 `BeginPaint` 设的裁剪区**，否则只重绘了无效区域，其余像素还是上一帧的

### 其他

- `EDIT` 控件只认 `\r\n`：给 `\n` 会被渲染成空格，整段挤成一行
- `EM_SETBKGNDCOLOR` 发给 RichEdit 会**直接访问违例崩溃**（GUI 版没有控制台，完全看不见）
- `DrawText` 默认不透明背景模式，白字会盖在白底上 → 看起来"按钮上没有字"
- `AlphaBlend` 的 `BLENDFUNCTION` 是**按值传的 4 字节结构**，传指针会直接失败
- `EM_SETCUEBANNER` 的提示在控件拿到焦点时消失，而输入框一直是焦点状态 → 改成自己画占位提示

## 版本号

只改一处：`internal/version/version.go` 的 `Version`，然后跑 `build.ps1`。
它会出现在托盘右键菜单、托盘提示文字、`mgdic-cli.exe config` 三处。

## 目录

```
cmd/mgdic/        程序入口（ui / q / import / config / bench）
internal/lexicon/ 读 CSV、算归一化键与常用度、抽中文词串
internal/store/   SQLite：建库、指纹、查询
internal/query/   方向判断、降级顺序、结果渲染（含 query_test.go）
internal/winui/   Win32 原生窗口：热键、托盘、自绘圆角框、RichEdit 结果框
internal/config/  配置文件
internal/llm/     极小的 chat/completions 客户端（只有整句翻译用它）
internal/version/ 版本号
screenshot/       界面截图
```

## 已知不足

- **中文倒排索引有固有噪音**：`喜欢 → care for`（ECDICT 的 `like` 词条里没有"喜欢"这个释义）、
  `美丽 → pulchritude`（`beautiful` 的释义写的是"美丽的"，对不上）
- **只做了中英**：日语要接 EDRDG 的 JMdict（CC BY-SA 4.0），韩语接 krdict（自带中文释义），
  来源核实过，还没接
- 窗口圆角由 `SetWindowRgn` 裁出来，没有抗锯齿（要平滑得改成分层窗口）
