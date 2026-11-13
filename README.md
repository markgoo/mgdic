# mgdic

一个只做一件事的本地词典：**Alt+E 呼出小窗 → 输入词 / 词组 / 句子 → 看结果 → 复制走**。

词与词组完全离线（76 万词条），只有整句翻译才调用 DeepSeek。
exe 11MB、常驻内存 20MB、启动 0.2 秒。

![查英文](screenshot/02-english-word.png)

## 用法

| 操作 | 作用 |
|---|---|
| `Alt+E` | 呼出 / 收起（开关） |
| `Enter` | 翻译整句 |
| `Esc` | 收起 |
| 点圆角框 | 复制框里那块翻译 |
| 拖选下方文本 + `Ctrl+C` | 复制任意片段 |
| 点「AI 翻译」 | 无条件翻译当前输入（单词也行） |
| 托盘左键 / 右键 | 呼出词典 / 菜单 |

`Ctrl+A` / `Ctrl+C` / `Ctrl+V` / `Ctrl+X` 和输入框右键菜单都可用。

## 安装

从 [Releases](https://github.com/markgoo/mgdic/releases) 下载 `mgdic.exe`，双击。

**首次运行会自动下载词典数据（66MB）并建库（约 10 秒）**，之后每次启动 0.2 秒就绪。
想自己准备数据见下面的「数据」。

## 界面

结果区分两层：**圆角框**放能直接复制走的主信息，**只读文本框**放辅助信息（可拖选其中一段再复制）。

| 输入 | 圆角框（点一下复制） | 文本框（可拖选） |
|---|---|---|
| `apple` | 中文释义 | 词头 + 音标、考纲标签、词形变化 |
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

命中词条、词组、词形还原、拼写纠错、前缀提示 —— 全部离线。

![翻译结果](screenshot/05-translated.png)

## 配置

`config.json` 和 exe 同目录，首次运行自动生成：

| 键 | 默认 | 说明 |
|---|---|---|
| `api_key` | 空 | 填了才能翻整句；也认环境变量 `DEEPSEEK_API_KEY` |
| `base_url` | `https://api.deepseek.com/v1` | OpenAI 兼容接口都吃 |
| `model` | `deepseek-chat` | |
| `hotkey` | `alt+e` | 支持 `ctrl` / `shift` / `win` 组合与 F1–F24 |
| `timeout_ms` | `8000` | 整句翻译超时 |
| `dict_csv` | exe 同级 `ecdict.csv` | 建库的源 |
| `dict_db` | exe 同级 `mgdic.db` | 词典库 |
| `log` | `true` | 是否写启动日志；`false` 就完全不写文件 |

配置、词典库、日志全在 exe 同目录，整个文件夹拷到别的电脑直接能用，不写 `%APPDATA%`。

> `config.json` 里有 API key，别把整个文件夹公开分享。

## 数据

**本项目不分发词典数据。** 数据由使用者自行从上游获取，版权与许可归上游作者。

| | |
|---|---|
| 来源 | [ECDICT](https://github.com/skywind3000/ECDICT) |
| 许可 | MIT（版权归 ECDICT 作者） |
| 下载 | `https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv`（66MB） |
| 内容 | 76 万词条：音标、中英释义、词性、考纲标签（中考/高考/CET4-6/考研/GRE）、柯林斯星级、牛津 3000、语料词频、词形变化 |

把 `ecdict.csv` 放在 exe 旁边，程序启动时会自动建库（也可以让程序自己下载，见「安装」）。

> mgdic 只认 CSV 这一种格式。ECDICT 还提供 Stardict / MDX 等格式，那是给别的词典软件用的，
> mgdic 不读。

## 从源码构建

需要 Go 1.21+：

```powershell
.\build.ps1
```

产出两个 exe：

| 文件 | 用途 |
|---|---|
| `bin\mgdic.exe` | GUI 子系统，双击没有控制台窗口，只出托盘图标 |
| `bin\mgdic-cli.exe` | 控制台版，给 `q` / `import` / `config` / `--selfcheck` 用 |

```powershell
.\bin\mgdic-cli.exe q apple        # 命令行查词
.\bin\mgdic-cli.exe import         # 手动建库
.\bin\mgdic-cli.exe config         # 看配置与库状态
.\bin\mgdic-cli.exe --selfcheck    # 窗口起不来时用它看每一步的成败
```

## 查询的降级顺序

**英文**：精确命中 → 词形还原（`runned`→`run`）→ 去连字符 → 拼写纠错
（`applicaton`→`application`，按编辑距离 1 展开候选）→ 当句子逐词 → 前缀提示。

**中文**：贪心切词（最长优先）→ 每段只给最可能的 1 个英文词
（候选噪音很大：查"苹果"会带出法语词 `pomme`、缩写 `app.`）。

## 目录

```
cmd/mgdic/        程序入口（ui / q / import / config / bench）
internal/lexicon/ 读 CSV、算归一化键与常用度、抽中文词串
internal/store/   SQLite：建库、指纹、查询
internal/query/   方向判断、降级顺序、结果渲染
internal/winui/   Win32 原生窗口：热键、托盘、自绘圆角框、RichEdit 结果框
internal/config/  配置文件
internal/llm/     极小的 chat/completions 客户端
internal/version/ 版本号
docs/             补充文档
screenshot/       界面截图
```

## 已知不足

- **中文倒排索引有固有噪音**：`喜欢 → care for`（ECDICT 的 `like` 词条里没有"喜欢"这个释义）、
  `美丽 → pulchritude`（`beautiful` 的释义写的是"美丽的"，对不上）
- **只做了中英**：日语要接 EDRDG 的 JMdict（CC BY-SA 4.0），韩语接 krdict（自带中文释义），
  来源核实过，还没接
- 窗口圆角由 `SetWindowRgn` 裁出来，没有抗锯齿

## 更多

- [Win32 静默失败的坑](docs/win32-pitfalls.md) —— 这个项目踩过的一批"不报错、只能靠看"的问题
- [多语言词典来源核实](docs/multilingual-dict-sources.md)
