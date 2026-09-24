# OHOS Go 工具链 v1.27.1-ohos

**这是 `golang/go` 的 fork**，把 **OpenHarmony (OHOS)** 加成受支持的 `GOOS`。
基线是上游 **go1.27.1**（`VERSION` 文件是权威），OHOS delta 叠在其上。

**这是第一个不带 `-beta` 的发布。** 升格的理由只有一条：**最后一块「结论只在别的树上成立」
的空白被补上了** —— C 层（编译器测试集）此前只在 beta1 那棵树上跑过，这一版**首次在交付物
本身上跑通**（见「验证到什么程度」）。**这不是覆盖面变大了**：能力边界与 beta2 逐条相同，
仍在下面的「已知问题」里写明。**edge 掉在边界外时表现为一段看不懂的链接器/加载器报错**，
而不是明确的功能缺失。

## 这个 cut 相对 beta2 改了什么

**代码改动集中在一个文件**：`misc/go_openharmony_exec/main.go`，也就是那个只在
「宿主 `go test` 把设备测试二进制推上设备」这条路径上被调用的 `-exec` 包装。
**编译器与其余工具链文件逐字节没动**（`bin/go` = `9cfddd979ec1c64f…`，与 beta2 相同）。

三件事：

1. beta2 的包装在**还没编出设备工具链的树上**会往 stderr 写一行
   `building the openharmony/arm64 toolchain (one time)`。Go 自己的 testdir 把被测二进制的
   stdout 与 stderr **并进同一个缓冲区**比较，于是这一行会让**第一个跑到的用例**凭空失败。
   **开发树上永远看不见它**（那棵树早把设备工具链编好了），是 C 层在交付物上重跑才现形的 ——
   见已知问题 **7d**。
2. 收口之后、发版之前清了一轮代码 review，**在同一个文件里又挖出 5 条**，形状与上一条同源：
   触发条件都只在交付环境下才满足（cgo 缺席、设备拒绝 push、设备上的目录被清掉……）。
   逐条见 **7e**。
3. 文档与判据侧：打包验收从一条「必须为空」扩成五条独立判据（字节数、`_exec` 成员数、
   **整棵** `bin/` 的路径检查、摘要真跑一次 `-c`）；打包配方补上「源码树要先装 `_exec` 包装」
   这一步（此前它依赖「那棵树上恰好装过」）；`runtests.sh` 补了 SDK 存在性门禁 ——
   此前 SDK 缺席会让每个 cgo 包 FAIL，而 FAIL 的形状和 port 缺陷一模一样。

| | beta2 | 本次 `v1.27.1-ohos` |
|---|---|---|
| 包装往 stderr 写通知 | 会（仅限未编出设备工具链的树） | **不会**（只在 stderr 是人盯着的终端时才写） |
| 包装里「只在交付环境才走到」的分支 | 5 处，无覆盖 | **全部有断言**（该包用例 4 → 8 个） |
| `bin/go` / `gofmt` | — | **逐字节相同** |
| `bin/go_openharmony_*_exec` | — | **不同**（上面这些修法的落点就在它里面） |
| C 层是否在交付物上跑过 | **否**（只在 beta1 那棵树上） | **是**（首次） |

> 换句话说：**这是一次「证据补齐 + 一个包装修复」，不是一次能力升级。**
> 从 beta2 升上来不用改任何构建脚本；`go version` 两版都打印 `go1.27.1-ohos`。

## 更早：beta2 相对 beta1 改了什么（2026-09-23 已发布）

| | beta1 | beta2 |
|---|---|---|
| 宿主预编译包 | 只有 darwin/arm64 | **darwin/arm64 + linux/amd64** |
| `go version` | `go1.27.1`（与上游逐字相同，分不出来） | **`go1.27.1-ohos`** |
| amd64 的纯 Go 构建 | 恒失败（`cannot handle R_AMD64_TLS_GD`） | 变成一句可读的报错，见已知问题 7b |
| darwin/arm64 包体 | 167 MiB | 69 MiB |

**1. 新增 linux/amd64 宿主预编译包。** 目标平台是 `openharmony`，宿主只要跑得动 Go 就行 ——
Linux 服务器上交叉编译是很常见的用法，所以这一版同时发两份宿主包。
两份都**在各自宿主上全量重建并实测过**（见「验证到什么程度」），不是交叉产出的。

**2. `go version` 现在带 `-ohos` 后缀。** beta1 的发布物打印的是裸 `go1.27.1`，与上游官方
发行版逐字相同 —— 下游只靠版本串分不出自己用的是哪一棵。现在
`go version go1.27.1-ohos linux/amd64` 一眼可辨。`VERSION` 文件同步为 `go1.27.1-ohos`。

**3. amd64 的纯 Go 构建不再撞一段不明所以的链接器错误。** 这是上一版留的真缺陷（已知问题 7b）：
`GOARCH=amd64` + `CGO_ENABLED=0` 必然死在 `cannot handle R_AMD64_TLS_GD ... when linking internally`。
修法是 amd64 每次构建都强制外部链接（照上游 `android/amd64` 的先例），于是失败提前成一句
`openharmony/amd64 requires external (cgo) linking, but cgo is not enabled`。
**代价是 amd64 的纯 Go 构建也要 `CGO_ENABLED=1` + SDK clang**；**arm64 完全不受影响**。

**4. 两条对使用者零影响的修复**（只为让仓库与发布物一致）：

- 补回 10 个被 `.gitignore` 的 `*.[56789ao]` 吞掉的上游二进制 testdata
  （`cmd/objdump`、`go/internal/gccgoimporter`、`go/internal/gcimporter` 的测试因此转绿）。
  **只影响 Go 自己的测试套件，不影响编出来的产物。**
- 仓库里的 `go.env` 现在是 `GOTOOLCHAIN=local`，与发布物逐字一致。此前仓库里是上游的
  `auto`，靠打包时排除 `go.env` 兜着 —— 发布物行为没错，但「仓库不是唯一真相」是个雷，已拆。

**5. 仓库新增宿主侧 CI**（`.github/workflows/ohos.yml`）：PR 与 push 在 ubuntu-latest +
macos-14 双宿主上重建并跑门禁包，夜间跑一次 `./all.bash` 全量。**对使用者没有直接影响**，
只是让「这棵树还能自己立起来」这件事有自动证据。

## 拿到

两份预编译树（**免 `./make.bash`**），挑你宿主的那份：

| 宿主 | 文件 | 大小 |
|---|---|---|
| macOS Apple Silicon | `go1.27.1-ohos-darwin-arm64.tar.gz` | 69.3 MiB |
| Linux x86-64 | `go1.27.1-ohos-linux-amd64.tar.gz` | 72.3 MiB |

各带一个 `.sha256`（**字节级精确的凭据是它，不是上面这个约数**）。

> **两份资产于 2026-09-24 在各自宿主上重新打包了两次**（先修 7d，再修 7e 的 5 条 review
> 缺陷并重打；darwin 在本机、linux 在 238 那台）。与 beta2 那两份相比，**工具链里变了的
> 只有宿主侧的 `-exec` 包装** —— `bin/go`、`bin/gofmt`、编译器、stdlib **逐字节相同**
> （`bin/go` = `9cfddd979ec1c64f…`）。所以从 beta2 升上来**不需要动任何构建脚本**，
> `go version` 也逐字相同。
>
> **树指纹**（定义见 `misc/openharmony/runtests.sh`：`bin/` 顶层文件的 sha256 汇总，**不含子目录**）
> 分别是 darwin `d681e0a3d13e…`、linux `5b46c7d7242b…`。
> **别拿指纹论证「换包没换编译器」** —— 指纹把那个包装也算在内，所以它必然变；
> 要论证那件事得看单个 `bin/go` 的哈希。指纹的正确用途只有一个：
> **判断「跑出结论的那棵树」与「你手上这棵树」是不是同一套工具链**。

> **证据包**（`v1.27.1-ohos-evidence.tar.gz`，随本 Release 附上）：B 层 262 行
> `results.tsv`（含每包的指纹列）+ C 层四个分片日志与驱动脚本，全部出自两份资产的
> 打包源树。

```bash
shasum -a 256 -c go1.27.1-ohos-darwin-arm64.tar.gz.sha256   # Linux 用 sha256sum -c
cd ~ && tar xzf /path/to/go1.27.1-ohos-<宿主>.tar.gz         # → ~/go1.27.1-ohos
export PATH="$HOME/go1.27.1-ohos/bin:$PATH"                  # 是功能开关，见下
```

**先读 `docs/ohos-toolchain-install.md`（在树里）** —— 三条硬前提（`bin` 上 `PATH`、`CC` 写
SDK clang 绝对路径、`go.env` 的 `GOTOOLCHAIN=local` 不要改）各对应一个**指向错误方向**的报错，
不知道就查不出来。

> **包体比 beta1 小了一半（167 MB → 69 MB）不是缺东西。** 包里只打 `pkg/tool` + `pkg/include`
> —— 与上游官方发行包形状一致；`bin/ go.env VERSION src/ test/ api/ lib/ misc/ doc/` 一个不少。
> 包里不装 `pkg/` 下的构建中间产物。

> **「免编译环境」指的是不用自己 `./make.bash` 造 Go 工具链**，不是不需要 SDK。
> 只要你编的是 **cgo** 产物（下游几乎都是），就仍然需要 OHOS SDK 的 clang 与 sysroot，
> `CC` 还得写成 SDK 里 clang 的**绝对路径**。纯 Go 的 arm64 产物才不需要 SDK。

## 支持矩阵

| | |
|---|---|
| 宿主 | **darwin/arm64、linux/amd64**，两份都是实测过的（见下）。其他宿主可自行 `make.bash` |
| 目标 | `openharmony/amd64`、`openharmony/arm64` |
| 设备侧执行 | 模拟器实测；商用真机的 `sh` 域不能 exec 未签名二进制 |

> **「支持」的粒度不一样，别当成一样**：`arm64` 是**构建期 + 运行期都验过**（模拟器上跑了
> 262 个有测试的包）；`amd64` 只验到**构建期** —— 编译、链接、shlib gcprog 解码，
> 即宿主侧可验的全部，**运行期零证据**（无 amd64 设备，模拟器 `127.0.0.1:5555` 是 aarch64）。
> 早先「amd64 有条静默错值路径」的说法**是误读，已推翻**；两架构的 shlib 形状实测逐字段相同，
> 危险情形是 `Exitf` 而非静默。证据见 `docs/ohos-full-test-plan.md` §5.5。

> **amd64 目标要求 `CGO_ENABLED=1`**（beta2 起是硬性的，见已知问题 7b）。
> 且 `CC` 的 `--target` 要换成 `x86_64-linux-ohos`。

## 验证到什么程度

前三条（A/B/C）各自独立，**结论不互相背书**（这是本项目反复吃过的教训）；
后两行是**跑在真实下游场景里**的证据，其中最后一行由下游自己产出：

| 层 | 覆盖什么 | 结果 |
|---|---|---|
| **A** 宿主编译器自测 | 编译器/链接器在宿主上的正确性 | **linux/amd64：377 ok / 0 FAIL**（`GIT_CONFIG_GLOBAL=/dev/null`）；**darwin/arm64：1 FAIL**，是使用者自己 `~/.gitconfig` 的 `http(s).proxy` 把测试的 `127.0.0.1` 也代走了（`codehost`），同一包在代理摘掉后转 `ok`。**基线没坏** |
| **B** 交叉编译 + 设备执行 | 262 个**有测试的** std 包能不能编、能不能在设备上跑 | beta1 树：239 PASS / 23 FAIL / 0 TIMEOUT / 0 WRAPPER；**在交付物上重跑过两轮，都是 240 PASS / 22 FAIL / 0 / 0**（2026-09-23 修完 7c、2026-09-24 修完 7d，见下） |
| **C** `test/` 编译器测试集 | 2734 个编译/链接/运行用例 | **2739 RUN / 132 FAIL**（**在交付物上跑的**；与开发树逐字相同，见下） |
| **端到端** | 真实下游项目构建 | `v2rayHM/scripts/build_libxray_ohos.sh` 退出 0，产物过全部检查 |
| **真机长稳** | 下游载体在**真机**上长时间跑 Go 编出的 c-shared（唯一一条由下游产出的证据） | 30 分钟连续：PID 一次未变、RSS/Threads/goroutine 无单调上升；**工具链指纹与 darwin 资产同源**（该指纹是 `bin/` 顶层哈希，只对 darwin 那份成立） |

**A 层是在 beta2 这棵树上跑的**，之后的 delta 只有 `go.env`、文档、CI，以及 7c/7d/7e 那几处
（`build.go` 补 `-trimpath`、`main.go` 拆出 `gorootFromExecutable`、`main.go` 给通知加
`noteToHuman` 门，以及 7e 的 5 条降级分支修复 + 新增 `devicecmd_test.go`）—— **全部只改宿主侧的
`-exec` 包装与其用例**，不动宿主编译器。
可复核的硬证据：`bin/go` 与 beta2 那份资产**逐字节相同**（`9cfddd979ec1c64f6f60bd455000e7b9b94302e7107d65f5a008d827f48b04cd`），
变的是 `bin/go_openharmony_*_exec`。所以这一层的结论没变；它的**宿主基线重认排在 CI
runner 换代之后**（roadmap ⑩.3）。

> 包装改了两轮之后，**工具的树指纹会变**（那是个 `bin/` 顶层的哈希，把包装算在内），
> 而 `bin/go` 的单值 sha256 不变。要论证「这一轮没碰编译器」请用**后者**，用指纹会被带偏。

**B 层：beta1 树那轮之外，又在交付物上重跑过两次，两次都不是复现，都抓到了新东西。**

| 轮次 | 树 | 结果 | 抓到了什么 |
|---|---|---|---|
| 2026-09-23 | 解包出来的交付物（`ohos-test-results/release-tree/`） | 240 PASS / 22 FAIL / 0 / 0 | 已知问题 **7c** —— 包装嵌着构建机绝对路径，12 个包在产物上假红，**而这 12 个包在开发树上全绿** |
| 2026-09-24 | 修完 7c/7d 的交付物（`ohos-test-results/release-tree-v1.27.1-ohos/`） | **240 PASS / 22 FAIL / 0 TIMEOUT / 0 WRAPPER，跳过已 PASS 0** | 已知问题 **7d** 的第二个爆炸半径被钉死：整轮 **262 行里没有一行** `go_openharmony_exec:`（见 7d） |
| 2026-09-24 夜 | 修完 **7e**（本文件末节那 5 条）的交付物（`ohos-test-results/release-tree-fixed-wrapper-v1.27.1-ohos/`） | **240 PASS / 22 FAIL / 0 TIMEOUT / 0 WRAPPER，跳过已 PASS 0** | 不是复现，是**另一条链路的收口**：B 层 262 个包全部由包装送进设备，包装改了就必须在新包上重跑。**22 个 FAIL 的名单与上一轮排序后 `diff` 为空**（逐包一致，§5.8 那张构成表逐行照用），整轮 `go_openharmony_exec:` 仍为 **0** |

**最后这一轮跑的就是这两份资产的打包源树**（指纹按 `bin/` 顶层算，文档改动不进指纹），
所以「跑出结论的那个包 = 你下载到的那个包」这条**本次成立** —— 见 7c、7d。
（**首版资产不成立**：它是修复前的，指纹 `54e81ae6…`。所以 2026-09-23 才重打。）

**本轮（7e 那次重取）的树指纹是 `d681e0a3…`**，与资产解包出来的树、以及本地安装树三方同值。
这一点值一提是因为它**不能**用「指纹没变」来论证：指纹算的是整棵 `bin/`，含那两个包装，
所以只要包装改了指纹必变。**换包装前后 `bin/go` 逐字节相同**（`9cfddd97…`）——
这才是「这一轮没碰编译器、A 层结论无需重认」的依据。

**一点克制**：23 → 22 之差**只对到了一处**（`go/internal/gccgoimporter` 从 FAIL 转 PASS，
对应 §7 那次 testdata 补回），两轮的 FAIL 集合**没有做过全量逐包对账**
（beta1 树那轮的 `results.tsv` 没进仓库），不要当成集合相等。
**但 7d 之后那两轮的 22 个 FAIL 是对过账的**：排序后 `diff` 为空，逐包相同。

**C 层已经在交付物上重跑过了 —— 这就是本次升格的全部理由。** 之前那一轮是 beta1 树上量的，
当时只能靠「delta 不改 arm64 代码生成」**推断**它对新树也成立，而**推断不是实测**。
2026-09-24 把它挪到交付树上跑完（`ohos-test-results/c-layer-release-tree-v1.27.1-ohos/`）：

| | 修前交付树 | 本次交付树 | §5.7 开发树 |
|---|---|---|---|
| RUN | — | **2739** | 2739 |
| FAIL | **133** | **132** | 132 |
| 其中 `R_ARM64_TLS_IE` | 125 | **125** | 125 |

**2739 与 132 两个数与开发树逐字相同** —— ⑩.4 要的答案就是这个：**那 125 条在新树上是同一条错**，
这条链接器空档与交付形态无关。修前多的那 1 条就是 7d（`abi/open_defer_1.go`，**恰好是当轮第一个
跑到的用例**），修后它转 PASS，四个分片里 `grep -c 'go_openharmony_exec:'` 全为 0。
全过程见测试方案 §5.10。

**C 层在最终交付物上又跑了一次，这一次是「同一个数」而不是「同一个区间」。** 7e 那 5 条改的正是
执行 C 层的那个载体（`bin/go_openharmony_*_exec`），所以必须在新包上重跑一遍才算数。重跑用的是
**同一个包解出来的树**（`ohos-test-results/c-layer-fixed-wrapper-v1.27.1-ohos/`），结果是
**每片 RUN/FAIL 都相同、132 个 FAIL 的名字与 §5.10 逐行 `diff` 为空** —— 比「总数相同」强一档。
两个佐证：四片日志里 `go_openharmony_exec:` 计数仍为 0（包装在新路径上没有多写一个字），
且那棵树是**冷的**（没有 `bin/openharmony_arm64/`），也就是说它完整走过了首次构建与降级分支 ——
这正是 §5.10 那轮没覆盖到的（B 层先跑，缓存已热）。**6m32s**，见 §5.11。

**B 层那 22 条 FAIL 全部落已知类**（逐包数出来的，见测试方案 §5.8）：环回类 17 包、
`sh` 域读不了 `/etc/ssl/certs`（`crypto/x509`）、unix socket/SCM_RIGHTS、设备权限模型、
设备无 C 编译器（`runtime` 的 9 个 cgo 测试）+ `runtime/TestTracebackSystem`。
（beta1 树那轮多一条 `go/internal/gccgoimporter`，已在 beta2 修好。）**没有一条是 port 缺陷。**
作为交付树上的**独立设备证据**：从发布用的这棵安装树跑
`GOOS=openharmony GOARCH=arm64 go test strings`，设备侧 `ok strings 24.874s`（真 PASS，
不是 `hdc` 那种打了 `[Fail]` 还 exit 0 的假绿）。

**C 层 132 条里有 125 条是同一个链接器致命错误**，而它是 **testdir 测试载体的限制，不是 port 缺陷**：
testdir 的 `rundir` 系列自己调 `go tool link` 且**不传 `-buildmode`**，链接器于是取 `exe`（非 PIE），
而 OHOS 的运行时是 iscgo 构建、stdlib 里带 `R_ARM64_TLS_IE` —— 非 PIE 的内部链接没实现这条重定位。
**用户走 cmd/go 永远撞不到**（`platform.DefaultPIE` 对 openharmony 为 true，默认就是 PIE）。
证据链和剩下 7 条的逐条定性见 `docs/ohos-full-test-plan.md` §5.7。

**端到端证据**（`build_libxray_ohos.sh`，libxray v26.9.9 + SSR 插件；**与 B/C 层同属 beta1 那棵树**）：

- 工具链：`go version go1.27.1 darwin/arm64`（那棵树还没有 `-ohos` 标记）；
  `GOOS=openharmony`、`-buildmode=c-shared`、`-trimpath=true`
- 产物 35978936 字节，与仓库里已提交的 `prebuilt/arm64-v8a/libxray.so` 逐项属性一致
  （ELF 头只差 section-header offset，导出集完全相同）
- `nm -D` 导出集恰为 `CGoFree` + `CGoInvoke`
- `PT_TLS` 段、`R_AARCH64_TLSDESC` 重定位、`GOOS=openharmony` 字符串均在
- v2rayHM 仓库 `git status --porcelain` 为空（脚本只写自己 gitignore 的 `build/`）

**这一层对本次交付的两份资产依然成立**：从那棵 beta1 树到现在，delta 里没有一处动到 c-shared
的产出路径，`bin/go` 与 beta2 那份资产逐字节相同（见上）。
但你自己的项目请**用你实际拿到的这棵树重编一次**（`OHOS_GO_ROOT` 指过去即可）。

**真机长稳（2026-09-23，由下游 v2rayHM 产出）** —— A/B/C 三层都是「一次跑通」，
而 B 层还跑在 `sh` 权限域 + 模拟器上。缺的那一格是「Go 编出的 c-shared 在真机 HAP 进程里
长时间跑稳不稳」，真实流量与真实前后台切换只有下游能产出。结果：

- 设备 `HED-AL00`（HUAWEI Pura 80 系列），**API 24**，内核 `HongMeng Kernel 1.12.0 aarch64`，
  **页 4 KB**、**用户地址空间 39 位**（两个数都不给我们惊喜，也不给我们答案 —— 见下）
- 被测产物是用 `~/go1.27.1-ohos` 重编的 `libxray.so`（35,978,936 B / `161a2de0…`），
  工具链指纹 **`743723f32866…`**
- 10 分 25 秒 ×11 次采样：VmRSS 117.5–140.4 MB 带内振荡（净 −0.9%）、Threads 只在 24/26 跳、
  **PID 11/11 全同**；前后台切换 ×3 回来 PID 全不变、后台时段隧道仍在走流量；
  收尾 5 分钟负载 +28.35 MB（不是「活着但废了」）。补采的 Go 内部计数：goroutine 前 2 分钟涨完、
  后 4 分钟平在 688–694，收尾时 RSS 从 128 MB **掉到 75.8 MB**（会把页还给 OS）
- **溯源**：载体量的工具链指纹 `743723f32866…` = 它当时用的 `~/go1.27.1-ohos`（darwin）
  安装树 = **当时那份 darwin-arm64 资产解包出来的 `bin/`**，三方同值。
  **别把这个指纹拿去对新资产**：那个口径算的是**整棵 `bin/`**，里面含 `-exec` 包装，
  而 7c/7d/7e 三次修的正是包装 —— 现在的 darwin 资产指纹是 `d681e0a3d13e…`。
  **要论证「换包没换编译器」得落到单个 `bin/go` 上**：新旧两份逐字节相同
  （`9cfddd979ec1c64f…`）。所以这份真机数据测的那个编译器，
  与你下载到的这份 darwin 包里的编译器是**同一个字节序列**；
  而 v2rayHM 走的是 ArkTS → N-API → cgo → c-shared，**根本不经过那个包装**。
  （**linux 那份不在这条链上**：它的 `bin/` 是 Linux 二进制，指纹本来就不同。）
- **不能读成什么**（载体自己如实标的，这里原样转述）：只有「30 分钟无异常」，**不是「无泄漏」**
  （慢泄漏要小时级）；`fd` 数与 `faultlog` 因设备无 root **整项取不到**；
  三轮里后台进程一直在收数据 ⇒ OHOS **没有**冻结这个 VPN 常驻进程，
  **「冻结 → 解冻」那条路径没被覆盖**；**16 KB 页那个悬着的问题这台机器答不了**（它是 4 KB 页）。
  只有一台设备、一个 OHOS 版本。方法与完整数据见 `docs/ohos-full-test-plan.md` §5.9

## 能力表（每条都有设备上的实测证据）

| 能力 | 结论 |
|---|---|
| cgo 可执行文件 | ✅ **默认即可**，不用加 `-buildmode=pie` |
| `-buildmode=c-archive` | ✅ 但**必须** `AR=$SDK/native/llvm/bin/llvm-ar`（见下） |
| `-buildmode=c-shared` | ✅ `dlopen(RTLD_NOW)`+`dlsym` 与 `-l<name>` 两条路都通 |
| `-buildmode=plugin` | ✅ |
| `-buildmode=shared` | ✅ |
| `-asan` | ✅ |
| **`-race`** | ❌ **不可用**，见已知问题 1 |
| `-msan` | ❌ 不可行（SDK 无 `libclang_rt.msan*`，且 MSan 要插桩过的 libc） |
| SVE 汇编 | ✅ 需 `GOEXPERIMENT=simd`（编码逐字节相符；硬件执行未验） |
| 应用域内 bind/listen 127.0.0.1 | ✅（**`sh` 域测不出来**，用 HAP 夹具在应用域验的） |
| x509 系统根 | ✅ **应用域直接可用**，不用设任何 env（`sh` 域测不出来，用 HAP 夹具验的） |
| x509 **用户自装**根 | ❌ 应用域读不到 `/data/certificates/user_cacerts`（EACCES），见已知问题 2 |

完整表格、每条的证据串、以及背后的机制在树里的 `docs/go-upgrade-guide.md` **§4.2**。

## 已知问题

**排在前面的更可能咬到你。** 编号沿用 beta1，方便对照。

### 1. `-race` 不可用（不开门）

设备用户地址空间只有 **39 位**（ASan 自报 `HighMem [0x002000000000, 0x007fffffffff]`，
探针 `vma_bits_stack=38`；真机与模拟器都是），而 Go 的 race runtime 硬编在 48 位布局上。
开了只会把构建期一句清楚的报错换成运行期 sanitizer 崩溃，所以**明确拒绝**。

**这不是「平台物理限制」，是决策** —— `-race` 是开发期诊断工具，交付产物永远不带它
（慢 5~20 倍、内存翻 5~10 倍）。上游立场（firecracker#3514 关成 not-a-bug、golang/go#29948 同）
就是「要 48 位就平台自己调内核配置」，Go 不会为小地址空间适配。

**要查竞态请在宿主上跑**：`GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go test -race ./...`
（竞态是源码级属性，与目标架构基本无关）。原生胶水层另可走 C++ 侧 `-fsanitize=thread`。
完整的四层闸与替代路径见 `docs/go-upgrade-guide.md` §4.2。

### 2. x509 系统根：公共 CA 可用，**用户自装的不可用**

本条前半段的旧结论（「取不到系统根，用 `SSL_CERT_FILE` 兜」）**是错的** ——
那是把测试载体的限制当成了平台的限制。

那条 `open /etc/ssl/certs: permission denied` 是在 **`sh` 权限域**（`uid=2000`，
`hdc shell` 跑测试用的域）里报的。应用域读得到 —— HAP 夹具（`misc/openharmony/loopbackhap/`，
进程 `uid=20020077` / `u:r:app:s0`）实测：

| Go 实际做的（`crypto/x509/root.go`） | 应用域结果 |
|---|---|
| `os.ReadDir("/etc/ssl/certs")` | OK，1 个条目 `cacert.pem` |
| 该条目**不是**同目录软链（否则被 `readUniqueDirectoryEntries` 剔掉） | `lstat → symlink=false` |
| `os.ReadFile("/etc/ssl/certs/cacert.pem")` | OK，191450 字节，开头即 `-----BEGIN CERTIFICATE-----` |
| `AppendCertsFromPEM`（同一份字节在宿主上跑） | **125 张全部解析成功** |

⇒ `roots.len() > 0`。**上游那 6 个 `certFiles` 在 OHOS 上一个都不存在，但
`certDirectories[0] = "/etc/ssl/certs"` 正好命中 OHOS 的 bundle 目录 —— 所以零 delta 是对的。**

**下游不用设 `SSL_CERT_FILE`。** 宿主设了也确实到不了设备（包装的 env 白名单只对测试有效），
别再照那条旧处方走。

**但用户自装的根取不到。** OHOS 把用户 CA 放在 `/data/certificates/user_cacerts`
（`lstat` 说它存在、不是软链），而应用域 `open` 它得 EACCES。上游给 Android 打的那两行
（`/data/misc/keychain/certs-added`）是这个位置的正解，但**照抄过来对 OHOS 无效**。
要支持得走 OHOS 的 cert framework（`@ohos.security.cert`）+ cgo，是**真特性不是补丁**，本次不做。

实践含义：**给公共 CA 签发的主机做 TLS 代理，开箱可用、零配置**；
要让用户装自签 CA（抓包调试、内网自建 CA）则不行。

### 3. 非 PIE 的内部链接不支持 `R_ARM64_TLS_IE`

manifest 表现是 `link: cannot handle R_ARM64_TLS_IE (sym runtime.load_g) when linking internally`。
**cmd/go 不会走到这里**（默认 PIE），只有自己调 `go tool link` 且不传 `-buildmode` 时才会 ——
testdir 就是唯一已知的触发者。**不要手工把 `-buildmode` 留空调用 `go tool link`。**

### 4. `hdc shell` 传不了 NUL

`-exec` 包装的回程在第一个 NUL 处截断，其后的字节一起丢。影响面是「测试输出里含 NUL」
（`test/nul1.go` 因此必挂）；**产物与正常 stdout 不受影响**。要修得改成设备侧 stdout 重定向
到文件再 `hdc file recv`，动的是执行路径核心，本次不做。

### 5. 真机不能跑设备侧测试

商用真机的 `sh` 域不能 exec `/data/local/tmp` 下的未签名二进制（`Permission denied`），
**换加载器绕过也无效**。这只影响**测试方式**（本次发布的设备侧结论全部出自模拟器），
不影响你编出来的产物 —— 产物是给 HAP 加载的。

### 6. 不要在设备上跑 `runtime/TestTracebackSystem`

它 `open` 的是**编译期绝对路径**，镜像整棵 GOROOT 也救不了（除非 `-trimpath` 构建）。
登记为已知不可跑。

### 7. 【beta2 已修】testdata 缺口

beta1 记的是「`testdata/importsar.gox` 在上游 HEAD 里就不存在」—— **那是误诊**。
真实原因是 **fork 导入时丢了 10 个上游跟踪的二进制 testdata**，被 `.gitignore` 的
`*.[56789ao]` / `*.a[56789o]` 吞掉（这些文件在上游是 `git add -f` 进去的，一旦丢失，
`git add -A` 永远加不回来，`git status` 也不报缺）。已按上游 blob sha **逐字节校验**补回；
全树现在与上游 go1.27.1 的文件存在性**零缺失**。
复现与泛化检查命令见 `docs/go-upgrade-guide.md` §3.5。

### 7b. 【beta2 已修】`openharmony/amd64` 的默认构建必然链接失败

beta1 上的表现是 `link: cannot handle R_AMD64_TLS_GD (sym net.SplitHostPort) when linking internally`，
**绕法是把 `CGO_ENABLED` 设成 `1`**。beta2 已修（amd64 每次构建都强制外部链接），
失败提前成一句可读的报错，**代价是 amd64 的纯 Go 构建也需要 `CGO_ENABLED=1` + SDK clang**
（与上游 android/amd64 同形）；**arm64 完全不受影响**。
机制、实测产物形状（`PT_INTERP = /lib/ld-musl-x86_64.so.1`、0 条 TLSDESC、lld 把描述符松弛成 LE）
见 `docs/go-upgrade-guide.md` §4.3。

### 7c. 【beta2 已修】设备测试用的 `-exec` 包装曾带着构建机绝对路径

> **历史条目，留档用。** 这是 beta2 **首版**资产的问题（2026-09-23 同日同名重打后已修，
> 打出来的两份计数全为 0）。留着它是因为它同时是一条方法论记录：
> **「在交付物上重跑一层」这个动作抓出来的第一个缺陷** —— 同一轮测试在开发树上全绿。
> （7d 是第二个，同因同源：**开发树比交付树多带状态**。）

**先说影响面：不影响交叉编译。** 你拿这个包编 HAP 里的 `.so`、编 cgo 可执行文件，
**完全不受影响** —— 那是 `bin/go` 干的活，而 `bin/go` 是干净的（绝对路径计数 0）。

受影响的只有**在设备上跑 Go 自己的测试**（`GOOS=openharmony go test`，走 `-exec` 包装），
且**只在你把包解到别的路径时**才发作。症状是 **12 个包**在设备上假红：

**已在首版资产上核实过**（2026-09-23；该文件随后已被替换掉）：下载首版
`go1.27.1-ohos-beta2-darwin-arm64.tar.gz`，
sha256 = `f5084bc17472c16c6b227472a47191ada1c2b6335bcc3f6152bba30f0c919528`（与当时 release 上的
摘要一致），解包后 `bin/go` 与 `gofmt` 都是 **0 处**绝对路径，而**两个 `_exec` 包装各 366 处**
（绝对路径形态的 `.go` 串各 174 条），指向 `/Users/xiphis/go1.27.1-ohos/{misc,src}/...`。

| | |
|---|---|
| 包里那个文件 | `bin/go_openharmony_{arm64,amd64}_exec`，嵌着 **366 处** `/Users/xiphis/go1.27.1-ohos/...` |
| 对照 | 同一个包里 `bin/go` 是 0 处（release 标志生效了）—— **只查 `bin/go` 的检查看不见它** |
| 发作条件 | 解包到构建机目录以外的任何地方 |
| 症状 | 每处 `../../testdata/...`、`../testdata/...`、`../../lib/time/zoneinfo.zip` 都 ENOENT |
| 实测命中 | 183/264 那轮里 **12 个包**（`grep -l 'no such file or directory' logs/*.log` 数出来的）：`compress/flate`、`compress/lzw`、`compress/zlib`、`crypto/internal/fips140test`、`go/parser`、`go/types`、`image/gif`、`image/jpeg`、`image/draw`、`internal/godebugs`、`internal/testenv`、`internal/zstd` |
| 最直白的证据 | `internal/testenv` 的日志里，设备侧 cwd 是 `/data/local/tmp/go_openharmony_exec/testenv.test-91720/cwd/` **+ 宿主的绝对路径** —— 相对化失败就地留下了形状 |

机制：包装里的 `findGoroot` 当时优先信 `runtime.GOROOT()`（**构建期烧进去的**路径，
而 `runtime.GOROOT()` 的官方注释恰好就是在警告这件事：「二进制被拷到别的机器后，
构建时的 root 不再有意义」）。拿到错误的 host GOROOT 之后，
`deviceCwdFor` 算出的相对路径带一堆 `../..`，判定为「在树外」，
于是退化成 `pushSourceTree` —— **只镜像包目录**，跨目录的 testdata 自然读不到。
同步失败是**非致命**的（只打一行 stderr），所以它一路静默到测试失败，
看起来像 port 缺陷而不是夹具缺陷。

根因在构建侧：`cmd/dist` 的 release 标志（`-trimpath -ldflags=-w`）是通过 `GOFLAGS` 注入的，
而包装是 `build.go` 里一条单独的 `goCmd` 编出来的，**吃的不是那份 GOFLAGS**；
`§6.7` 的安装树刷新又在装好的树里裸编了一次，于是把 `/Users/xiphis/go1.27.1-ohos` 烧了进去。

**树里已修**（**现在下载到的两份资产已含全部五条**）：
1. `cmd/dist/build.go` 给包装的构建加 `-trimpath` —— 顺带让 tar 包不再泄出构建目录；
2. `findGoroot` 改成**只信包装自身位置**，彻底不查 `runtime.GOROOT()`，
   并把它拆成 `gorootFromExecutable` 以便测试；
3. `TestGorootFromExecutable` 钉住「自身位置优先、拿不准就不猜」这条规则；
4. `§6.7` 的手装命令补 `-trimpath`，并加一条**打 tarball 前必过**的「整棵 `bin/` 绝对路径计数为 0」；
5. CI 的打包自检从只查 `bin/go` 改成遍历整棵 `bin/`（见下条：连那条判据本身也写错过）。

**顺带修掉的一条判据缺陷（这个要单独说）**：这条缺陷的检查原先写成
`strings "$f" | grep -cF "$PWD"`。它**只在「站在构建树自己的路径上」时有效** ——
解包到别处之后 `$PWD` 不是构建路径，计数**恒为 0**。实测：**首版 beta2 资产**，
用它自己的目录跑那种写法得 `0`（看着干净），换成下面这条得 `174`（缺陷现行）：

```bash
for f in bin/*; do [ -f "$f" ] || continue
  printf '%6s  %s\n' "$(strings "$f" | grep -cE '^/[^ ]*\.go$')" "$f"; done   # 每行必须是 0
```

判据路径无关，所以它**同时能用来看别人给的包**。三处文档（安装文档、guide §6.7/§6.8、CI）
已按这一条改。

**修完之后**：那 12 个包在产物上**全数转绿**，整轮 262 行 / **240 PASS / 22 FAIL** /
指纹 `743723f32866…`（测试方案 §5.8）。跑这一轮的树就是 **beta2 那两份资产**的打包源树
（指纹按 `bin/` 顶层算），所以 beta2 的「跑出结论的那个包 = 你下载到的那个包」是成立的 ——
首版资产不成立，它是修复前的（指纹 `54e81ae6…`），这才有那次重打。
**v1.27.1-ohos 的两份资产出自另一棵树**（修了 7d，随后又修 7e 并再打一次，见 7e 节）：
7d 那轮的指纹是 `bc958b2dca13…`，最终 darwin 资产是 `d681e0a3d13e…`。

这条是**「在交付物上重跑一层」这个动作抓出来的** —— 同一轮测试在开发树上全绿。
它也正是 `misc/openharmony/runtests.sh` 的树指纹从 `bin/go` 扩成整棵 `bin/` 的原因
（当时两棵树 `bin/go` 逐字节相同，是那个包装不一样）。

### 7d. 【本次已修】包装在刚解包的树上会往 stderr 写一行，让 C 层的第一个用例凭空失败

**影响面比 7c 小得多，但它暴露的是同一类问题，所以记下来。**

`-exec` 包装发现 `bin/openharmony_arm64/` 不存在时会**现编**一套设备工具链，同时往 stderr
打一行 `go_openharmony_exec: building the openharmony/arm64 toolchain (one time)`。
交互时这是好事（第一次要等几分钟，得告诉人一声）。**但在交付树上会咬人**：

| | |
|---|---|
| 触发条件 | 树里没有 `bin/openharmony_arm64/` —— **tarball 里必然没有**（按设计排除，它是缓存） |
| 为什么开发树看不见 | 开发树早就在某次跑测试时把它编出来了，那行通知不会再出现 |
| 咬到谁 | `cmd/internal/testdir` 把被测二进制的 **stdout 与 stderr 并进同一个缓冲区**比较（`testdir_test.go:644-646`：`cmd.Stdout = &buf; cmd.Stderr = &buf`），于是「output should be empty… Instead saw」 |
| 为什么**只有一条** | 那行只出现一次（编完即止），所以**只有第一个跑到的用例**中招 —— 也就是**随机哪一个**，于是 C 层的 FAIL 数**不可复现** |
| 本轮实况 | 交付树上 `abi/open_defer_1.go` 报出那一行原文；同一棵树在开发树上 132 FAIL、交付树上 **133** |

**第二个爆炸半径（同一次查出）**：`misc/openharmony/runtests.sh:320` 把日志里任何以
`go_openharmony_exec:` 开头的行判成 **WRAPPER 失败**（rc 125）。所以这一行不只让 testdir 假红，
还可能把 B 层某个包误判成「夹具坏了」。**修完这轮 B 层实测：0 条 WRAPPER。**

**修法 —— 不是删掉通知，是限定它写给谁看。** 加一个 `noteToHuman(f *os.File) bool`：
只有 `f.Stat().Mode()&os.ModeCharDevice != 0` 才写（终端是字符设备；管道、普通文件不是）。
断言在 `misc/go_openharmony_exec/stderr_test.go`：管道 / 普通文件 / `os.DevNull` 三例。
第三例是**正控制**：少了它，把函数改成 `return false` 也能过，而那是「删掉通知」
不是「限定通知」。

> **首版只收了一半（2026-09-24 交付前 review 查出，本次一并修掉）。** 当时只把「编设备工具链」
> 那一行收进 `noteToHuman`，**降级路径（GOROOT 同步失败那一行）漏在外面**。它和前者是同一类：
> 都只在人盯着终端时才有意义，漏掉就仍留着一个入口，能把一条输出正被比对的长跑变成假红 ——
> 而它的触发条件（设备拒绝 push、GOROOT 不可写）恰恰在**交付环境下比开发机上更容易满足**。
> 现在的界线是**按「写什么」划，不是按「哪一行」划**：所有通知一律过 `noteToHuman`，
> 唯一无条件写 stderr 的是 `fail()` —— 走到那儿运行已经废了，它的输出不是拿来比对的东西。
> 判据也随之从「读注释」变成了断言：`TestRunMainPushesSourceTree` 要求**整条降级路径下
> stderr 写入 0 字节**（同一条用例还要验「同步失败时包目录镜像仍在」，两件事一次覆盖）。

> **与 7c 并排读，教训是同一条：开发树比交付树多带状态。**
> 7c 多的是「构建路径恰好等于安装路径」，7d 多的是「设备工具链缓存已经暖了」。
> 两次都由**「在交付物上重跑一层」**抓出来，两次在开发树上都全绿。
> 这也解释了为什么这一轮的 C 层**必须**在解包树上跑：**它测的不是载体，
> 是「用户第一次拿到这棵树时会发生什么」**。

### 7e. 【本次已修】交付前 review 在同一个包装文件里又挖出 5 条

7d 修完之后、发版之前清了一轮代码 review。结果**5 条全部落在
`misc/go_openharmony_exec/main.go` 这一个文件里**，而且**没有一条能从开发树上看见** ——
它们全是「开发树带着交付树没有的状态」这一类的兄弟。列在这儿是因为它们同一个来源、
同一种形状，分开写会让人以为是五次独立的偶然。（其中 M1 **就是上面 7d 那条收口漏掉的
另一半**：不是新缺陷，是同一个缺陷的第二个入口。）

| # | 缺陷 | 交付环境为什么会咬到 | 修法与判据 |
|---|---|---|---|
| H1 | 可选的「编目标架构工具链」失败会**连带否决必须做的源码树镜像** | `amd64` 强制外部链接、要 cgo 与设备 C 工具链，**在没为设备构建过的树上必然失败** —— 于是 `../../testdata` 一类的读取从「跳过」变成 ENOENT，看起来像 port 缺陷 | 失败降级为警告并只丢掉 `bin`/`pkg` 两个归档，源码树照推；`TestSyncKeepsTheTreeWhenTheToolchainCannotBeBuilt`（两个变异都抓到：退回早退 / 照样发 bin+pkg） |
| H2 | 设备侧解包脚本用 `;` 串联，且**无条件**把「同步成功」的指纹写回宿主 | `tar` 半途失败后剩下的阶段照跑，留下一棵新旧混杂的树；而指纹是**永久**记录，下一次运行会跳过复制，于是永远答自一个残缺的树 —— 重试本可答对，这里答错 | 改成 `&&` 链 + 末尾 `__SYNC_OK__` 标记，宿主**验到标记才写指纹**；`TestSyncScriptStopsAtFirstFailure`、`TestSyncDoesNotCacheAFailedExtract` |
| M1 | 降级通知没走 `noteToHuman`（即 7d 的收口漏了一半） | 见上条引用块 | 断言整条降级路径 stderr **0 字节** |
| M2 | `cd` 失败被伪装成「测试退出码 1」 | 宿主以为设备上的目录还在、设备其实丢了（重装 HAP、清 `/data/local/tmp` 之后）—— 一整层全红且**没有一个字解释原因** | `cd` 失败时由 shell 走 `exit 125` 且**不写**退出码哨兵，于是既有的「拿不到退出码」诊断自行生效；`TestCdGuard` |
| M3 | 拼进 shell 串的设备路径没加引号 | 路径里有空格（`os.TempDir()` 在部分设备配置下会）就整条命令错位 | 每个设备路径过 `shellQuote`；`file send` 的 argv **故意不动**（那是 exec 的参数字组，不经 shell） |

> **这一轮学到的形状：包装的降级路径是「在 `go test` 下看不见」的。**
> 你运行 `go test` 时，包装正被 `testdir` 当成被测二进制，它的 stderr 被并进比对缓冲区 ——
> 于是**任何一句额外输出都是假红，而不是提示**；反过来，把通知全关掉又会让交互使用变哑。
> 唯一的出路就是 7d 那条界线（终端才写），以及**单独跑包装自己的测试**去覆盖降级分支 ——
> `misc/go_openharmony_exec` 的用例从 4 个增到 8 个，新增的 4 个全在
> 「同步失败 / 工具链编不出 / cd 丢失 / 换合成失败」这些正常路径永远不会走到的分支上。

### 8. 覆盖度的坦白

C 层 125 条 `R_ARM64_TLS_IE` FAIL 挡在**链接期**，而它们里绝大多数是 `rundir` 用例 ——
**编译发生了，运行没有**。所以那一轮的覆盖是「编译器」而不是「运行期」。
（这一轮换到交付树上跑，结论一字未变：还是这 125 条；**「跑在交付物上」解决的是可信度，
不是覆盖面** —— 两件事别混。）
「sweep 全红」和「sweep 全绿」一样会藏东西：上游 go1.26.5 那代就有一个静默的 FIPS 缺陷
在**全绿的全量轮里**躲了很久，直到做定向验证才现形。

**运行期这一格现在有四份来源**（B 层那一轮、环回 HAP、下游 xray 端到端、真机 30 分钟），
**但四份都是「短时、单次、单设备」** —— 合起来仍然撑不起「运行期验证充分」这句话。
缺口是**慢泄漏**（要小时级）、**被冻结后解冻**（要能拿到冻结前后的快照）、
**16 KB 页设备**（我们一台都没有），以及**多 OHOS 版本**。写在这里是为了让这份覆盖
**看起来比它实际强**这件事不会发生。

## 与上一代（v1.26.5）的差异

- 基线 go1.27.1（上一代是 go1.24.5）
- **默认 PIE 已落码**：上一代的树编 cgo 可执行文件会在 `init()` 之前 `Signal 11`，
  这一代默认 buildmode 就产出可运行的 PIE（配 musl 解释器，二者缺一不可）
- `-exec` 包装镜像**整棵 GOROOT**（上一代只推包目录），B 层 FAIL 36 → 23
- 已知问题里，`-race` / `-msan` / 真机 exec 三条与上一代相同（都是平台限制）；
  **x509 那条被推翻并改写了**（见已知问题 2）

## 反馈

问题提到本仓库的 issue。**下游项目（v2rayHM / sing-box / xray）本身的问题不要提到这里** ——
那些提到各自的仓库。
