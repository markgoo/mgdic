# 开源双语词典数据源核实（SpeeD / JMDict·EDICT / KRDICT）

抓取时间：2025-12-14（本次会话）。全部为只读网络访问，未改动本机其他文件。
说明原则：**只写我实际抓到原文的内容**；抓不到的一律标注「未能核实」，不臆测许可条款。

---

## 0. 一句话结论

| 来源 | 站点当前状态 | 许可（原文要点） | 商用 | 中文方向 | SQLite 导入 |
|---|---|---|---|---|---|
| SpeeD Project（speeproject.org） | **已下线**：域名不解析、RDAP 查无、搜索引擎与 Wayback 均无有效结果 | **未能核实** | 未能核实 | 未能核实 | 未能核实 |
| JMDict / EDICT（EDRDG，NeoCrawler/Jim Breen 系） | 在线（edrdg.org） | **CC BY-SA 4.0**（"Creative Commons Attribution-ShareAlike Licence (V4.0)"） | 允许，原话 "NO restriction placed on commercial use" | **无中文释义**（只有 eng/ger/rus/hun/dut/spa/fre/swe/slv） | 需 XML 解析器，或改吃 JSON 发行版 |
| KRDICT（한국어기초사전，국립국어원） | 在线（krdict.korean.go.kr；`krdict.or.kr` 不解析） | **CC BY-SA 2.0 대한민국**（저작자표시-동일조건변경허락 2.0） | 允许，原话 "누구나 상업적 용도까지 포함하여 자유롭게 이용할 수 있으며" | **有中文**（11 语翻译中含중국어；API `trans_lang=11`） | XML（逐词条 zip / Open API 返回 XML），Go 标准库可解析 |

**中文方向总结（详见 §4）**：只有 KRDICT 明确提供「该语言词条 ↔ 中文释义」；JMDict 没有中文词条释义，日语必须走英文中转；SpeeD 无法核实。

---

## 1. SpeeD Project（http://www.speeproject.org/）

> 任务里列的语种对：英语 ↔ 越南语、印尼语、马来语、泰语、俄语、希腊语、葡萄牙语、土耳其语 —— 这份清单来自任务描述本身，**我没能从任何在线来源复核它**（站点已下线）。

**站点抓取结果：失败，且判定为「已下线」而非「临时故障」**，证据链：

1. 直接抓取 `http://www.speeproject.org/`、`http://speeproject.org/`、`https://speeproject.org/` 全部 `TypeError: fetch failed`（DNS/连接层面失败，不是 404 页面）。
2. Google DNS-over-HTTPS：`www.speeproject.org` 与 `speeproject.org` 均为 **Status 3 = NXDOMAIN**（`org.` 域由 `v0n0.pir-ns.org` 返回权威应答）→ 域名已不在注册状态。
3. RDAP（PIR）：`https://rdap.publicinterestregistry.org/rdap/domain/speeproject.org` → **HTTP 404 "Object not found"** → `.org` 注册记录已撤销。
4. Wayback Machine：`https://archive.org/wayback/available?url=http://www.speeproject.org/` → `"archived_snapshots": {}`（无快照）；CDX 精确匹配 `http://www.speeproject.org/` → 空数组 `[]`；CDX 域匹配查询期间多次撞上 **HTTP 503 "Internet Archive: Temporarily Offline"**，未能补跑成功。
5. 搜索引擎：Bing 引号查询 `"speeproject"` / `"speeproject.org dictionary download"` 均无该站结果（返回的全是 speedtest、Yahoo 之类的噪声，且 Bing 抓取页标 "Content was generated with AI"，引号未生效，可靠性差）。Wikipedia 搜索 API 查 `"SpeE/D" dictionaries English` → `totalhits: 0`。GitHub API 仓库搜索 `q=speeproject` → `total_count: 0`。
6. SourceForge 镜像猜测：`https://sourceforge.net/projects/speed/` 存在，但是 **xnn（C++ 通信库，GPLv2）**，与本词典无关。

**结论**：

- 下载地址：**未能核实**（站点无存档、无镜像可确认）。
- 数据格式（.dictx / .dsl / .tsv / .html）：**未能核实**。我只能说：我在 GitHub 仓库搜索里没能定位到叫 `speed`/`dict-format` 的现成镜像（GitHub 用户 `speed` 存在，博客是 newcrawler.com，但与 SpeeD 词典无直接证据关联，不宜当作镜像）。
- 许可证条款、署名要求、商用许可：**未能核实**。
- 词条量：**未能核实**。
- 建议：把它当作「需人工找旧存档/线下包」的来源，不要写进 SQLite 初始化脚本。

**不确定项**：Wayback 当时整体 503（Internet Archive 服务临时离线），所以「无抓取记录」这条证据本身还有一次补跑余地；如果日后 archive.org 恢复且 CDX 给出抓取列表，本站节需重写。

---

## 2. 日语：JMDict / EDICT（EDRDG，Jim Breen / NeoCrawler 系）

`https://www.cs.monash.edu.au/~jm/` 已不可靠；当前官方地址是 **EDRDG**：`https://www.edrdg.org/`（词典页 `https://www.edrdg.org/jmdict/j_jmdict.html`，Wiki 文档 `https://www.edrdg.org/wiki/index.php/JMdict-EDICT_Dictionary_Project` → 现实际页面 `https://www.edrdg.org/wiki/JMdict-EDICT_Dictionary_Project.html`，Wiki 首页已迁到 `https://www.edrdg.org/dokuwiki/doku.php`）。旧 Monash FTP（`ftp.monash.edu`）本次抓取失败。

### 2.1 下载与格式

官方分发主站：**`http://ftp.edrdg.org/pub/Nihongo/`**（另有 rsync），主文件（抓自 wiki 文档页原文列表）：

- `JMdict.gz` — 完整 JMdict（含 English, German, French, Russian, Spanish, Hungarian, Slovenian, Dutch 释义）
- `JMdict_e.gz` — 仅英文释义
- `JMdict_b.gz` — 仅英文释义且剔除约 7,000 条 JMnedict 人名条
- `JMdict_e_examp.gz` — 上者 + Tanaka 语料例句
- `edict.gz` — 传统 EDICT（legacy）
- `edict2.gz` — 扩展的 EDICT2

格式（同页原文要点）：

- **JMdict：XML / UTF-8（Unicode ISO-10646）**，DTD 见 `dtd-jmdict.xml`；
- **EDICT：一行一条**，`KANJI [KANA] /(pos) gloss/gloss/.../`，**JIS X 0208 编码、EUC-JP 封装**；
- **EDICT2：EDICT 扩展格式**，多条头形/假名 + 交叉信息字段，`EUC-JP`（JIS X 0208 与 JIS X 0212）；
- **EDICT_SUB：同 EDICT 格式**，约最常见的 20% 条目。
- 相关项目：ENAMDICT/JMnedict（约 74 万专有名词）、KANJIDIC/KANJIDIC2、RADKFILE/KRADFILE、COMPDIC。

**关于任务里点名的 `jmdict.sql.gz` / `jmdict.csv.gz` / `edict.jis` / `jae.icss`：未能核实。** 原因：FTP 首页与 `00INDEX.html` 我都能取到 HTTP 200，但抓取器报「HTML content omitted: unable to convert safely」读不出正文，`00INDEX.txt` 是 404，Monash 镜像站连接失败，Bing 引号搜索不可靠，所以我无法确认这四个文件名当前仍在 FTP 上。

### 2.2 文件大小与词条量（有实测数字的部分）

EDRDG 自身的 FTP 目录列表我读不出大小，但上游转换项目 **`scriptin/jmdict-simplified`** 的 release 正文给了实测表（我抓的是 `releases/latest`，release 标记 `3.6.2+20261109214828`，其内记录日期以站点记录为准）：

| 数据集 | 文件大小（release 正文） | 词条数（按语言） |
|---|---|---|
| JMdict（XML 源） | 115MB（120,621,416 字节） | all/eng 219,040；ger 129,132；rus 69,409；hun 41,901；dut 41,827；spa 34,311；fre 15,336；swe 14,518；slv 8,776 |
| JMdict with examples | 69MB（72,168,365 字节） | all/eng 219,040 |
| JMnedict | 146MB（153,007,831 字节） | all/eng 743,690 |
| Kanjidic | 15MB（15,641,823 字节） | all 13,108；en 10,384；es 2,505；fr 2,066；pt 1,944 |

同 release 里 JSON 发行包（GitHub 资产 `size` 字段，实测字节数）：

- `jmdict-all-….json.zip` 25,120,859 B（`.tgz` 25,120,825 B）
- `jmdict-eng-….json.zip` 11,543,033 B
- `jmdict-eng-common-….json.zip` 1,445,502 B（"common-only" 小得多的版本）
- `jmdict-examples-eng-….json.zip` 14,173,749 B
- `jmnedict-all-….json.zip` 13,452,713 B
- `kanjidic2-all-….json.zip` 1,552,881 B；`kanjidic2-en-….json.zip` 1,253,441 B
- `kradfile-….json.zip` 106,080 B；`radkfile-….json.zip` 134,219 B
- 单语言版我抓到的还有：`dut` 3,341,493 B、`ger` 7,027,312 B、`spa` 1,494,498 B、`fre` 650,775 B、`swe` 453,381 B（`hun`/`rus`/`slv` 在响应被截断的中段，未逐条复述）

历史词条量参考（wiki 文档原文）：1999 年 EDICT 文件约 6 万条目（"about 60,000 entries"）。

### 2.3 许可：原文要点（`https://www.edrdg.org/edrdg/licence.html`，逐句摘）

- **许可类型**："The dictionary files are made available under a Creative Commons Attribution-ShareAlike Licence (**V4.0**)."（配图与链接指向 `creativecommons.org/licenses/by-sa/4.0/`）
- **适用范围**（原文列表）：JMDICT、EDICT、ENAMDICT、COMPDIC、KANJIDIC2、KANJIDIC（有特殊条件）、KANJD212、EDICT-R、RADKFILE/KRADFILE；"…and any data files which are derived from them"（由这些文件派生的数据文件同样受此许可）。
- **署名（저작자 표시）**：软件包/服务器/App 用到这些文件时，须在文档、宣传材料、站点里"acknowledge the usage and source of the files"，并"provide copies of the documentation and licence files"（打包不便时给链接即可，例如 `https://www.edrdg.org/wiki/index.php/JMdict-EDICT_Dictionary_Project`）。
- **商用（原文）**："provided the conditions above are met, there is NO restriction placed on commercial use of the files. The files can be bundled with software and sold for whatever the developer wants to charge. **Software using these files does not have to be under any form of open-source licence.**" → 商用**允许**，且不传染你的程序代码。
- **署名 + ShareAlike 的实际含义**：ShareAlike 只约束「你发布这些文件的摘录/改编本」必须沿用同许可（"You must attribute the work…" / "you may distribute the resulting work only under the same, similar or a compatible licence"）；许可还明确"Users of material from the files must **NOT** claim copyright over that material"。
- **额外义务（本地程序容易踩的坑）**：第 4 节 "Updating Dictionary Versions"："there must be a procedure for regular updating of the data…at least once a month. **Failure to keep the versions up-to-date is a violation of the licence** to use the data."
- 另有 KANJIDIC/KANJIDIC2 的第三方署名清单（SKIP 码、Pinyin、四角号码、韩字读法、De Roo 码等），以及"建议有收益时向该组捐款（PayPal）"。
- KANJIDIC/KANJD212 旧 GPL 条件已于 2000-03-25 撤销（第 7 节）。

### 2.4 SQLite 导入 / 是否需要我写解析器

- **JMdict XML**：UTF-8，但含 DTD + 实体声明（release 正文标"# of XML entities: 266"）。转换项目对原始 XML 的评价是："Original XML files are less than ideal in terms of format… the original format used some 'advanced' XML features, such as **entities and DOCTYPE**, it could be quite difficult to use in some tech stacks"，且"…no 'same as previous' implicit values"问题（原 XML 有"同上继承"语义）。→ 我若直吃 XML，需自己处理实体/DOCTYPE 与字段继承；`encoding/xml` 不一定够。
- **推荐路径**：直接吃 `jmdict-simplified` 的 JSON 发行（all 25MB / eng 11.5MB / eng-common 1.4MB，按语言切分），写一个 JSON→SQLite 的批量导入即可，**不需要 XML 解析器**；许可与原文件同（CC BY-SA 4.0，衍生文件沿用原许可，README 已如此声明）。
- **另一个现成件**：`FooSoft/jmdict`（Go 库，README 原文："JMDict is a simple library written in Go for parsing the raw data files for the JMDict… JMnedict… KANJIDIC"，作者吐槽 XML "deceptively annoying to work with"）→ 与本机 Go 技术栈对味，可作为解析器现成件（注意其 import 路径为 `foosoft.net/projects/jmdict`）。
- **EDICT/EDICT2**：一行一条、格式极简单，解析器很容易写；但**必须做 EUC-JP → UTF-8 转码**（我在文档里读到的是 EUC-JP 封装，不是 UTF-8）。
- 许可侧注意：CC BY-SA 的 ShareAlike 意味着**我若把词典摘录发布成自己的词表/辞典文本**，那份摘录也要 BY-SA；纯粹内部导入 SQLite 不受影响。

---

## 3. 韩语：KRDICT（한국어기초사전 / 국립국어원）

**当前官方地址**：`https://krdict.korean.go.kr`（대한민국 공식 전자정부，문화체육관광부 국립국어원）。
任务里写的 `https://krdict.or.kr` **本次抓取失败且 DNS NXDOMAIN**（Google DoH `Status: 3`）→ 该写法请视为「未能核实/已失效」。
根页面原文对本站的定义："‘한국어기초사전’은 … 쉬운 한국어 사전으로 **5만여 단어가 실려** 있습니다."（≈5 万词条）

它的姊妹件「국립국어원 한국어-외국어 학습사전」把基础词典的**表头与释义翻成 11 种语言**，根页面列出：러시아어、몽골어、베트남어、스페인어、아랍어、**영어**、인도네시아어、일본어、**중국어（韩国语-汉语）**、타이어、프랑스어。

### 3.1 许可：原文要点（`https://krdict.korean.go.kr/kor/kboardPolicy/copyRightTermsInfo`）

- "본 누리집에서 별도로 명시되지 않은 모든 자료는 '크리에이티브 커먼즈 **저작자표시-동일조건변경허락2.0 대한민국 라이선스**'에 따라 배포되며 이용될 수 있습니다."（CC BY-SA 2.0 KR，链接 `creativecommons.org/licenses/by-sa/2.0/kr/`）
- 商用："…자료는 **누구나 상업적 용도까지 포함하여 자유롭게 이용할 수 있으며** 저작자의 특별한 허가가 필요하지 않습니다."（商用允许，无需特别许可）
- 条件两条：**저작자 표시**（必须署名）与 **동일조건변경허락**（改了必须以同许可再分发）。
- 重要例外（原文）："텍스트 자료가 아닌 이미지, 동영상, 음악, 소리, **발음** 등 다중 매체(멀티미디어) 파일 자료는 상업적 이용 허용 여부와 저작물 변경의 허용 여부가 **개별적으로 설정**되어 있으므로 … 저작권 정책에 따라 … 이용해야 합니다." → 文本是 CC BY-SA 2.0 KR，但**音频/图片/发音是逐件设定**。
- 佐证（社区转换仓库 README，两处独立一致）："이 저작물은 크리에이티브 커먼즈 저작자표시-동일조건변경허락 2.0 대한민국 라이선스에 따라 이용할 수 있습니다."（changwoo/krdict-converted、spellcheck-ko/korean-dict-nikl-krdict）
- 音频再分发注意（spellcheck-ko README 原文）："2019년 7월 현재 국립국어원 사이트로 링크되어 있는 발음 파일은 모두 **재배포 불가능**하다는 답을 받았습니다." → 官方口头答复：发音文件不可再分发。

### 3.2 格式 / 下载 / 大小

- **站点入口**：`/kor/mainAction`（词典主界面，我已抓取 200）；功能条目含 "**사전 전체 내려받기**"（词典整体下载）与 "개발 지원(Open API)"。
- **整体下载的格式与大小**：**未能核实**——该入口是 JS 弹窗（`downloadPopup()`），我猜的两个直连路径（`/kor/dicDownload/dicDownloadMain`、`/kor/mainAction` 旧式路径）返回 404，没读到弹窗里的真实链接与文件大小。
- **社区侧的确定事实**（changwoo/krdict-converted README 原文）：上游数据是「국립국어원 한국어기초사전（`https://krdict.korean.go.kr/`）에서 **다운로드한 XML 데이터**」，批量下载会拿到「**XML 파일이 들어 있는 zip 파일**」；转换输出格式：`dict.xdxf.dz`（XDXF，GoldenDict 等）与 `dictd`；构建需要 `make, xsltproc, dictzip`。
- **Open API**（`/kor/openApi/openApiInfo` 已抓取全文）：请求 URL `https://krdict.korean.go.kr/api/search`，**XML 返回**，需 `key`（32 位十六进制认证密钥），日调用上限 **50,000**（错误码 010 "Daily API Limit Exceeded"）；`translated=y` + `trans_lang` 选翻译语言，映射原文：전체(0), 영어(1), 일본어(2), 프랑스어(3), 스페인어(4), 아랍어(5), 몽골어(6), 베트남어(7), 타이어(8), 인도네시아어(9), 러시아어(10), **중국어(11)**；返回字段含 `trans_word` / `trans_dfn`（表头与释义的翻译）。示例 XML 里演示的是英语+日语两条 `translation`，字段结构与 11 语言参数表在原文里都在。
- 另有一条常被误认的「KRDICT」：`krdict.py` 库 README 把它叫 "Korean Learners' Dictionary"，并写 "provided by the National Institute of Korean Language"，API 文档链接即 `krdict.korean.go.kr/openApi/openApiInfo` → 与上面是同一个站，不是另一个域名。

### 3.3 SQLite 导入 / 是否需要我写解析器

- 逐词条 **XML**（zip 批量）：**Go 标准库 `encoding/xml` 足够**（无 DTD 实体、无「同上继承」这类怪招），解出后 `INSERT` 即可。
- 若只走 **Open API**：日限 5 万次 + 必须存 `key`，属于「在线查」而不是「离线词典」；导入 SQLite 时要走分页/限流，**建议还是吃批量 XML 下载**。
- 现成转换件：changwoo/krdict-converted 已把 XML 转成 **XDXF / dictd**（仓库许可 CC BY-SA 2.0 KR），可省一个解析器，但那仓库已 `archived`（API 标记 `archived: true`），维护需自担。
- 许可侧：CC BY-SA 2.0 KR 的 ShareAlike 同样只咬「你发布的衍生词典文本」；音频条目要单独确认。

---

## 4. 这些来源里有没有「多语言 → 中文」方向？

**结论：只有 KRDICT 确定提供；日语必须走英文中转；SpeeD 无法核实。**

1. **KRDICT = 有中文方向（已核实）**。站内有「국립국어원 한국어-외국어 학습사전」的 **한국어-중국어（韩国语-汉语）** 版本（根页面 / 帮助页 / Open API 的 `trans_lang` 参数表三处独立一致，API 里 `중국어 = 11`），且释义也有翻译（`trans_dfn`）。所以韩语侧不需要英文中转。
2. **JMDict = 没有中文释义（已核实，反向证明）**。release 正文「Entries by language」列表与转换项目 README 的语言码清单里，语言只有 `all/eng/ger/rus/hun/dut/spa/fre/swe/slv`；wiki 文档「OTHER LANGUAGES」一节也原文列举 German/Russian/Hungarian/Spanish/Dutch/Swedish/French/Slovenian 的条目数——**中文不在其中**。JMdict 的定位是"Japanese as the pivot language"的多语言库，英文是基础层（`JMdict_e.gz` 即"only English glosses"）。→ 日中方向需另找来源或经英文中转。
3. **SpeeD = 未能核实**：站点已下线，`英文 → 该语言` 是否成立、有没有中文层，都不能从网络侧证实。
4. 附带发现（不属上面三个来源，但可能有用）：KRDICT 的 11 语翻译里含**越南语、印尼语、泰语、俄语、希腊语？（不含希腊语；含塔伊语/阿拉伯语/蒙古语/西班牙语/法语/日语/中文）**——即「한어총/국립국어원」这套覆盖 SpeeD 语种里的越南语/印尼语/泰语/俄语/印尼语等，但它是「韩语↔X」，不是「英语↔X」，且中文在 API 里可选；SpeeD 语种中的**马来语、希腊语、葡萄牙语、土耳其语**在本次核实范围内我没找到带中文释义的开源双语来源。
5. 另一个可当「中转表」的东西：**KANJIDIC2** 的 `Entries by language` 有 `en/es/fr/pt`（含葡萄牙语！）与 **RADKFILE/KRADFILE**（部首分解）、**COMPDIC**、**JMnedict**（人名）——都是同一 EDRDG 许可（CC BY-SA 4.0）下的配套件，导入同一个 SQLite 时许可一致，可以一起进库。

---

## 5. 我实际访问过的 URL

成功取回内容（HTTP 200 或有意义应答）：

- `https://example.com/`（抓取通道探测）
- `https://www.edrdg.org/jmdict/j_jmdict.html`
- `https://www.edrdg.org/edrdg/licence.html`（许可原文）
- `https://www.edrdg.org/wiki/index.php/JMdict-EDICT_Dictionary_Project`（迁移告示）
- `https://www.edrdg.org/wiki/JMdict-EDICT_Dictionary_Project.html`（词典文档全文）
- `http://ftp.edrdg.org/pub/Nihongo/`、`http://ftp.edrdg.org/pub/Nihongo/00INDEX.html`、`http://ftp.edrdg.org/pub/Nihongo/??`（200，但正文未被抓取器转换，读不出内容）
- `http://ftp.edrdg.org/pub/Nihongo/00INDEX.txt`（404，用于试探索引文件格式）
- `https://krdict.korean.go.kr/`（根，多语言词典入口）
- `https://krdict.korean.go.kr/kor`（→ `/kor/mainAction`，词典主界面与「整体下载 / Open API / 저작권 정책」入口）
- `https://krdict.korean.go.kr/kor/kboardPolicy/copyRightTermsInfo`（저작권 정책 原文）
- `https://krdict.korean.go.kr/kor/help/helpList`（도움말 原文，5万词/11语言）
- `https://krdict.korean.go.kr/kor/openApi/openApiInfo`（Open API 原文：请求 URL、参数表、XML 字段、错误码）
- `https://raw.githubusercontent.com/scriptin/jmdict-simplified/master/README.md`
- `https://api.github.com/repos/scriptin/jmdict-simplified/releases/latest`（文件大小与词条数）
- `https://raw.githubusercontent.com/changwoo/krdict-converted/master/README.md`
- `https://raw.githubusercontent.com/spellcheck-ko/korean-dict-nikl-krdict/master/README.md`
- `https://raw.githubusercontent.com/omarkmu/krdict.py/main/README.md`
- `https://raw.githubusercontent.com/FooSoft/jmdict/master/README.md`
- `https://api.github.com/search/repositories?q=KRDICT&per_page=10`、`?q=jmdict&per_page=5`、`?q=dict-format&per_page=10`、`?q=speeproject&per_page=5`
- `https://api.github.com/users/speed`
- `https://sourceforge.net/projects/speed/`（验证 SourceForge 镜像猜测，结果是无关项目）
- `https://dns.google/resolve?name=www.speeproject.org&type=A`、`?name=speeproject.org&type=A`、`?name=krdict.or.kr&type=A`
- `https://rdap.org/domain/speeproject.org`（跨域到）`https://rdap.publicinterestregistry.org/rdap/domain/speeproject.org`（404）
- `https://archive.org/wayback/available?url=http://www.speeproject.org/`（无快照）
- `https://web.archive.org/cdx/search/cdx?url=http://www.speeproject.org/&…`（空数组）、`…url=speeproject.org&…`（503/超时）
- `https://en.wikipedia.org/w/api.php?action=query&list=search&srsearch=…&format=json`（0 命中）
- 搜索通道：`https://html.duckduckgo.com/html/?q=…`、`https://lite.duckduckgo.com/lite/?q=…`、`https://www.bing.com/search?q=…`（4 次）、`https://www.mojeek.com/search?q=…`（403 反爬）

抓取失败（记录在案，作为「站点不可达」证据）：

- `http://www.speeproject.org/`、`http://speeproject.org/`、`https://speeproject.org/`（全部 fetch failed）
- `https://krdict.or.kr/`（fetch failed，DNS 亦 NXDOMAIN）
- `https://ftp.edrdg.org/pub/Nihongo/`、`https://ftp.monash.edu/pub/nihongo/`、`http://ftp.monash.edu/pub/nihongo/00INDEX.html`（fetch failed）
- `https://timetravel.mementoweb.org/api/json?url=…`、`https://searx.be/search?q=…&format=json`、`https://r.jina.ai/…`（各 fetch failed）、`https://grep.app/api/search?q=speeproject`（429）、`https://searchcode.com/api/codesearch_I/?q=speeproject&per_page=10`（404）
- 会话内 `web_search` 工具不可用（provider `web-search-free` registered but unavailable），所以搜索全部改走上面的直连抓取通道。

---

## 6. 给本机程序（Windows，Go + SQLite）的可执行建议

1. **先接 KRDICT 与 JMDict**：两者许可都在原文里写明「商用允许 + 必须署名」，且都是 CC BY-SA 系（4.0 / 2.0 KR），导入同一个 SQLite 不冲突；记得在程序的「关于/来源」页里放署名与许可文件链接（EDRDG 对 App 的署名方式有具体说明，不是启动页一闪即过）。
2. **JMDict 用 JSON 发行、别用原始 XML**：省掉 DTD/实体/「同上继承」的解析器；EDICT/EDICT2 若要一起吃，记得 EUC-JP 转码。
3. **许可里的硬义务记得写进程序**：EDRDG 要求数据**至少每月更新一次**，否则算违反许可——这条对「本地词典程序」是真要求，不是客套。
4. **SpeeD 先按「需要线下存档」处理**：站点当前不可达（NXDOMAIN + RDAP 404），许可与格式在拿到存档前不要写死假设。
