# OHOS Go 工具链 v1.27.1-beta2

**这是 `golang/go` 的 fork**，把 **OpenHarmony (OHOS)** 加成受支持的 `GOOS`。
基线是上游 **go1.27.1**（`VERSION` 文件是权威），OHOS delta 叠在其上。

**beta 的含义**：目标平台（`openharmony/arm64`）在真实下游项目上编过、跑过、验过；
但覆盖面仍是「已知边界内可用」，边界在下面的「已知问题」里逐条写明。
**edge 掉在边界外时表现为一段看不懂的链接器/加载器报错**，而不是明确的功能缺失。

## 这个 cut 相对 beta1 改了什么

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
| macOS Apple Silicon | `go1.27.1-ohos-beta2-darwin-arm64.tar.gz` | 69 MiB |
| Linux x86-64 | `go1.27.1-ohos-beta2-linux-amd64.tar.gz` | 72 MiB |

各带一个 `.sha256`（**字节级精确的凭据是它，不是上面这个约数**）。

> **两份资产已于 2026-09-23 刷新（同名替换，`v1.27.1-beta2` 这个 tag 与提交都没动）。**
> 首版那两份的设备测试用包装带着**构建机绝对路径**（已知问题 7c），已从修好的树重打 ——
> 打包源是 `ohos-1.27-base` 分支上 2026-09-23 那次修复提交（`git log -1 --format=%h -S gorootFromExecutable`）。
> 变的只有那一个手装的包装二进制与几份文档；**工具链本体（`bin/go`、编译器、stdlib）逐字节相同**，
> 所以版本点没必要换。**在此之前的下载请重新下一次**，以新的 `.sha256` 为准。
> 对交叉编译本来就没有影响（受影响的只有设备侧跑 Go 自己的测试套件）。
>
> 两份资产的**树指纹**（定义见 `misc/openharmony/runtests.sh`：`bin/` 顶层文件的 sha256 汇总）
> 分别是 darwin `743723f32866…`、linux `2656478e91af…` —— 前者正是跑出 B 层 240/22 的那棵树，
> 后者是这次在 238 上重编包装后量的。


```bash
shasum -a 256 -c go1.27.1-ohos-beta2-darwin-arm64.tar.gz.sha256   # Linux 用 sha256sum -c
cd ~ && tar xzf /path/to/go1.27.1-ohos-beta2-<宿主>.tar.gz          # → ~/go1.27.1-ohos
export PATH="$HOME/go1.27.1-ohos/bin:$PATH"                         # 是功能开关，见下
```

**先读 `docs/ohos-toolchain-install.md`（在树里）** —— 三条硬前提（`bin` 上 `PATH`、`CC` 写
SDK clang 绝对路径、`go.env` 的 `GOTOOLCHAIN=local` 不要改）各对应一个**指向错误方向**的报错，
不知道就查不出来。

> **包体小了一半（167 MB → 69 MB）不是缺东西。** beta2 只打 `pkg/tool` + `pkg/include`
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

三层各自独立，**结论不互相背书**（这是本项目反复吃过的教训）：

| 层 | 覆盖什么 | 结果 |
|---|---|---|
| **A** 宿主编译器自测 | 编译器/链接器在宿主上的正确性 | **linux/amd64：377 ok / 0 FAIL**（`GIT_CONFIG_GLOBAL=/dev/null`）；**darwin/arm64：1 FAIL**，是使用者自己 `~/.gitconfig` 的 `http(s).proxy` 把测试的 `127.0.0.1` 也代走了（`codehost`），同一包在代理摘掉后转 `ok`。**基线没坏** |
| **B** 交叉编译 + 设备执行 | 262 个**有测试的** std 包能不能编、能不能在设备上跑 | beta1 树：**239 PASS / 23 FAIL / 0 TIMEOUT / 0 WRAPPER**；交付物上重跑（2026-09-23，修完 7c 之后）：**240 PASS / 22 FAIL / 0 / 0** |
| **C** `test/` 编译器测试集 | 2734 个编译/链接/运行用例 | **2739 RUN / 132 FAIL** |
| **端到端** | 真实下游项目构建 | `v2rayHM/scripts/build_libxray_ohos.sh` 退出 0，产物过全部检查 |

**A 层是在 beta2 这棵树上跑的**，之后的 delta 只有 `go.env`、文档、CI，以及 7c 那两处
（`build.go` 补 `-trimpath`、`main.go` 拆出 `gorootFromExecutable`）—— 后两者只改**包装自己**，
不动宿主编译器，所以这一层的结论没变；它的**宿主基线重认排在 CI runner 换代之后**（roadmap ⑩.3）。

**B 层：beta1 树那轮之外，2026-09-23 又在交付物上重跑了一遍**（`--toolchain` 指向解包出来的树，
落盘 `ohos-test-results/release-tree/`）。**这一轮不是复现，是发现了新东西** —— 它当场抓到
已知问题 7c（包装嵌着构建机绝对路径 → 12 个包在产物上假红），而那 12 个包在开发树上全绿。
修完后重跑：**240 PASS / 22 FAIL / 0 / 0**，262 行，每行都带同一个树指纹。

**这一轮跑的就是这两份资产的打包源树**（指纹按 `bin/` 顶层算，文档改动不进指纹），
所以「跑出结论的那个包 = 你下载到的那个包」这条**本次成立** —— 见 7c。
（**首版资产不成立**：它是修复前的，指纹 `54e81ae6…`。所以 2026-09-23 才重打。）

**一点克制**：23 → 22 之差**只对到了一处**（`go/internal/gccgoimporter` 从 FAIL 转 PASS，
对应 §7 那次 testdata 补回），两轮的 FAIL 集合**没有做过全量逐包对账**
（beta1 树那轮的 `results.tsv` 没进仓库），不要当成集合相等。

**C 层仍是 beta1 那棵树上测的，没在交付物上重跑 —— 这点如实标注。** 判断依据是两棵树的
delta 对 `arm64` 只涉及构建期的平台表、版本串与宿主侧测试数据，**不改代码生成**；
但这**是判断不是实测**，所以不要把它当成「beta2 的 C 层也全绿」。
作为交付树上的**独立设备证据**：从发布用的这棵安装树跑
`GOOS=openharmony GOARCH=arm64 go test strings`，设备侧 `ok strings 24.874s`（真 PASS，
不是 `hdc` 那种打了 `[Fail]` 还 exit 0 的假绿）。

**B 层那 22 条 FAIL 全部落已知类**（逐包数出来的，见测试方案 §5.8）：环回类 17 包、
`sh` 域读不了 `/etc/ssl/certs`（`crypto/x509`）、unix socket/SCM_RIGHTS、设备权限模型、
设备无 C 编译器（`runtime` 的 9 个 cgo 测试）+ `runtime/TestTracebackSystem`。
（beta1 树那轮多一条 `go/internal/gccgoimporter`，已在 beta2 修好。）**没有一条是 port 缺陷。**

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

**这一层对 beta2 依然成立**：beta1→beta2 的 delta 里没有一处动到 c-shared 的产出路径。
但你自己的项目请**用你实际拿到的这棵树重编一次**（`OHOS_GO_ROOT` 指过去即可）。

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
要支持得走 OHOS 的 cert framework（`@ohos.security.cert`）+ cgo，是**真特性不是补丁**，本 beta 不做。

实践含义：**给公共 CA 签发的主机做 TLS 代理，开箱可用、零配置**；
要让用户装自签 CA（抓包调试、内网自建 CA）则不行。

### 3. 非 PIE 的内部链接不支持 `R_ARM64_TLS_IE`

manifest 表现是 `link: cannot handle R_ARM64_TLS_IE (sym runtime.load_g) when linking internally`。
**cmd/go 不会走到这里**（默认 PIE），只有自己调 `go tool link` 且不传 `-buildmode` 时才会 ——
testdir 就是唯一已知的触发者。**不要手工把 `-buildmode` 留空调用 `go tool link`。**

### 4. `hdc shell` 传不了 NUL

`-exec` 包装的回程在第一个 NUL 处截断，其后的字节一起丢。影响面是「测试输出里含 NUL」
（`test/nul1.go` 因此必挂）；**产物与正常 stdout 不受影响**。要修得改成设备侧 stdout 重定向
到文件再 `hdc file recv`，动的是执行路径核心，beta 阶段不做。

### 5. 真机不能跑设备侧测试

商用真机的 `sh` 域不能 exec `/data/local/tmp` 下的未签名二进制（`Permission denied`），
**换加载器绕过也无效**。这只影响**测试方式**（本 beta 的设备侧结论全部出自模拟器），
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

### 7c. 【2026-09-23 重打资产已修】设备测试用的 `-exec` 包装曾带着构建机绝对路径

> **这条是 beta2 首版资产的问题，现在下载到的两份已经修好**（`bin/` 顶层四个文件计数全为 0）。
> 手上若是 2026-09-23 之前的下载，**重新下一次**就行 —— tag 没动，同名文件已替换。
> 下面是取证与机制，留档用。

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
指纹 `743723f32866…`（测试方案 §5.8）。跑这一轮的树就是本次两份资产的打包源树
（指纹按 `bin/` 顶层算），所以**「跑出结论的那个包 = 你下载到的那个包」这次是成立的** ——
首版资产不成立，它是修复前的（指纹 `54e81ae6…`），这才有这次重打。

这条是**「在交付物上重跑一层」这个动作抓出来的** —— 同一轮测试在开发树上全绿。
它也正是 `misc/openharmony/runtests.sh` 的树指纹从 `bin/go` 扩成整棵 `bin/` 的原因
（当时两棵树 `bin/go` 逐字节相同，是那个包装不一样）。

### 8. 覆盖度的坦白

C 层 125 条 `R_ARM64_TLS_IE` FAIL 挡在**链接期**，而它们里绝大多数是 `rundir` 用例 ——
**编译发生了，运行没有**。所以那一轮的覆盖是「编译器」而不是「运行期」。
「sweep 全红」和「sweep 全绿」一样会藏东西：上游 go1.26.5 那代就有一个静默的 FIPS 缺陷
在**全绿的全量轮里**躲了很久，直到做定向验证才现形。

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
