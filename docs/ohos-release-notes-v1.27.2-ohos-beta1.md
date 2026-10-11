# OHOS Go 工具链 v1.27.2-ohos-beta1

**这是 `golang/go` 的 fork**，把 **OpenHarmony (OHOS)** 加成受支持的 `GOOS`。
基线是上游 **go1.27.2**（`VERSION` 文件是权威，值为 `go1.27.2-ohos`），OHOS delta 叠在其上。

**beta 的含义**：目标平台（`openharmony/arm64`）在真实下游项目上编过、跑过、验过；
但覆盖面仍是「已知边界内可用」，边界在下面的「已知问题」里逐条写明。
**edge 掉在边界外时表现为一段看不懂的链接器/加载器报错**，而不是明确的功能缺失。

## 这个 cut 相对 v1.27.1-ohos 改了什么

| | v1.27.1-ohos | v1.27.2-ohos-beta1 |
|---|---|---|
| 上游基线 | go1.27.1 | **go1.27.2**（上游 2026-10-08 发布，46 个提交） |
| OHOS delta | — | **`net` 接口表修复**（见下第 2 条） |
| C 层已知 FAIL | 132 | 132（名单逐条相同；多出的 5 个上游新用例全 PASS，见「验证到什么程度」） |

**1. 上游 go1.27.2 合入。** 46 个提交，以安全加固为主，对下游直接相关的：

- **`net/http` × 10**：Trailer 也吃 header 限额、HPACK 编码器竞态崩溃、`ServeContent`
  的 Range 头大小限制、CONNECT 后连接复用/连接池损坏 ×2、HTTP/1 并发 Read/Close 死锁、
  畸形分帧头清除等；
- **`crypto/tls`**：ECH outer extension 引用校验；**`crypto/mlkem`**：FIPS 130-3 CAST 修复；
- **`cmd/go`**：**toolchain 下载无条件过 sumdb 校验**、fips140 的 go.sum h1 哈希内嵌；
- **`html/template`** × 2（`yield` 关键字、JS 上下文重置）；
- 其余：`os`/Windows ×3、runtime（Windows 线程、plugin itabs 去重、32 位 eventfd）、
  `cmd/compile` 尾调用与栈分配 ×5、`compress/flate`、`encoding/json` ×2 等。

合并按 `.claude/skills/merge-upstream/` 流程走：残留 diff 与 OHOS delta **逐字节相等**
（190 文件、+9223/−443），23 项特性锚点逐一核对在位。

**2. `net` 接口表修复第一次进入发布资产。** `e71d58c0735`（2026-10-05）修了三处：
`getifaddrs`/`socket` 双泄漏、ioctl 失败从静默吞掉改为 `continue`、sig 43 定案补注释。
`v1.27.1-ohos` 的发布资产打在该提交之前，**不含这三个修复**；从本版起包含。

## 拿到

两份预编译树（**免 `./make.bash`**），挑你宿主的那份：

| 宿主 | 文件 | sha256（前 12 位） |
|---|---|---|
| macOS Apple Silicon | `go1.27.2-ohos-beta1-darwin-arm64.tar.gz`（69 MiB） | `39b6f063271b` |
| Linux x86-64 | `go1.27.2-ohos-beta1-linux-amd64.tar.gz`（73 MiB） | `c5d824d9ed23` |

各带一个 `.sha256`（**字节级精确的凭据是它，不是上面这个约数**）。

> 两份资产的**树指纹**（解包后 `cat bin/* | sha256sum`）：darwin `7613e5ba52d7…`、
> linux `1f7ef5ae98e4…`。darwin 那棵就是跑出 B 层 240/22 与 C 层 2744/132 的解包树
> （证据 MANIFEST 里有同一行）；linux 那棵只做过构建期验收（版本、INTERP、7b 报错）。

```bash
shasum -a 256 -c go1.27.2-ohos-beta1-darwin-arm64.tar.gz.sha256   # Linux 用 sha256sum -c
cd ~ && tar xzf /path/to/go1.27.2-ohos-beta1-<宿主>.tar.gz          # → ~/go1.27.2-ohos
export PATH="$HOME/go1.27.2-ohos/bin:$PATH"                         # 是功能开关，见下
```

**先读 `docs/ohos-toolchain-install.md`（在树里）** —— 四条硬前提（`bin` 上 `PATH`、`CC` 写
SDK clang 绝对路径、`go.env` 的 `GOTOOLCHAIN=local` 不要改、目标为 amd64 时 `CGO_ENABLED=1`）
各对应一个**指向错误方向**的报错，不知道就查不出来。

> **「免编译环境」指的是不用自己 `./make.bash` 造 Go 工具链**，不是不需要 SDK。
> 只要你编的是 **cgo** 产物（下游几乎都是），就仍然需要 OHOS SDK 的 clang 与 sysroot，
> `CC` 还得写成 SDK 里 clang 的**绝对路径**。纯 Go 的 arm64 产物才不需要 SDK。

## 支持矩阵

| | |
|---|---|
| 宿主 | **darwin/arm64、linux/amd64**，两份都是各自宿主上重建并实测的（见下）。其他宿主可自行 `make.bash` |
| 目标 | `openharmony/amd64`、`openharmony/arm64` |
| 设备侧执行 | 模拟器实测；商用真机的 `sh` 域不能 exec 未签名二进制 |

> **「支持」的粒度不一样，别当成一样**：`arm64` 是**构建期 + 运行期都验过**（模拟器上跑了
> 262 个有测试的包）；`amd64` 只验到**构建期** —— 编译、链接、shlib gcprog 解码，
> 即宿主侧可验的全部，**运行期零证据**（无 amd64 设备，模拟器是 aarch64）。
> 且 `amd64` 目标要求 `CGO_ENABLED=1`，`CC` 的 `--target` 要换成 `x86_64-linux-ohos`。

## 验证到什么程度

前三条（A/B/C）各自独立，**结论不互相背书**（这是本项目反复吃过的教训）。
B/C 两层**都重跑在解包后的 darwin 交付物上**，不是开发树上：

| 层 | 覆盖什么 | 结果 |
|---|---|---|
| **A** 宿主编译器自测 | 编译器/链接器在宿主上的正确性 | **darwin/arm64：`./all.bash` 全量 ALL TESTS PASSED（460 ok / 0 FAIL，`GIT_CONFIG_GLOBAL=/dev/null`）**；linux/amd64 由 CI 门禁覆盖（合并提交与此前各轮同形状） |
| **B** 交叉编译 + 设备执行 | 262 个**有测试的** std 包能不能编、能不能在设备上跑 | **240 PASS / 22 FAIL / 0 TIMEOUT / 0 WRAPPER**（在交付物解包树上；与 v1.27.1-ohos 交付物基线逐字相同，22 条全是已知环境类） |
| **C** `test/` 编译器测试集 | 编译 + 链接 + 设备上运行 | **RUN 2744 / FAIL 132**（在交付物解包树上；2744 = 基线 2739 + 上游新增 5 用例且全部 PASS。132 条 FAIL 的**名单与 v1.27.1-ohos 交付物证据逐条 diff 为零**，`R_ARM64_TLS_IE` 恰 125 条不变。其中 `fixedbugs/issue10607.go` 一条随载体漂移：开发树上 PASS、解包交付树上以 `linkmode=external … cgo is not enabled` 复现——上轮同签名，是交付树上 cgo 使能状态不一致的**载体怪癖**，不是 port 缺陷） |
| **§4.2 能力链** | 九种能力逐项实测 | 全绿（见下表） |
| **端到端** | 真实下游项目构建 | `v2rayHM/scripts/build_libxray_ohos.sh` 用本交付物构建退出 0，产物过全部检查（`GOOS=openharmony` 字符串、`PT_TLS`、`R_AARCH64_TLSDESC`、`CGoInvoke;CGoFree;` 导出集） |

## 能力表（每条都有设备上的实测证据）

| 能力 | 结论 | 本轮证据 |
|---|---|---|
| split identity | `goos=linux` + `IsOpenharmony=true` | `probe` 上设备 |
| 纯 Go 可执行 | 默认 buildmode 产出 PIE + musl 解释器，上设备跑通 | 解包树构建 + 模拟器执行 |
| cgo 可执行 | 同上（`Signal 11` 回归哨兵） | `cgomin` → `cgo ok 42` |
| `c-shared` | `dlopen(RTLD_NOW)`+`dlsym` → 42 | `cdriver dlopen` → `Add(40,2)=42` |
| `c-archive` | 静态链 → 42（macOS 须 `AR=llvm-ar`） | `cdriver link` → 42 |
| `plugin` | `plugin.Open`→`Lookup` → 42 | `hostplug` + `plug.so` |
| `shared` | `-linkshared` 宿主 + 设备侧 `libstd.so` → 42 | 运行时 ABI 自检工作正常（推错版本库即报 `abi mismatch`） |
| `-asan` | 检出 heap-buffer-overflow 并 abort | `asandemo` → `ERROR: AddressSanitizer` + rc=2 |
| `-race` | ❌ 不开门（决策，见已知问题 1） | — |

## 已知问题

### 1. `-race` 不可用（不开门）

**「不移植」是决策，不是欠债。** 四层闸：平台登记无 openharmony case（构建期一句干净报错）、
`runtime/race/race.go` 的 build constraint、race runtime 是预编译 blob（无 C++ 源码）、
race 堆硬编在 824 GiB 区间而设备只有 39 位 VMA。上游立场：平台自己调 VMA，Go 不适配。
替代：竞态在宿主上验（`GOOS=linux` + `-race`）；原生胶水层走 C++ 侧 `-fsanitize=thread`。
何时重估：OHOS 改 VA/页大小配置时（16KB 页下必然 ≥42 位，进 Go 允许集）。

### 2. x509 系统根：公共 CA 可用，**用户自装的不可用**

应用域直接可用、不需要任何 env：`/etc/ssl/certs` 命中 OHOS bundle 目录，读链逐环验过
（`ReadDir` → 非 softlink → `ReadFile` 191450 B → `AppendCertsFromPEM` 125/125）。
唯一真缺口：`/data/certificates/user_cacerts`（OHOS 用户 CA）应用域 EACCES → 用户自装 CA
取不到。要做得走 OHOS cert framework + cgo，是真特性不是补丁。

### 3. 非 PIE 的内部链接不支持 `R_ARM64_TLS_IE`

模拟器/真机的 musl 加载器对 `-shared` 产物的 TLS 模型要求，导致部分 `test/` 用例
（125 条）在链接期报 `cannot handle R_ARM64_TLS_IE (sym runtime.load_g) when linking internally`。
这是 C 层 131 条 FAIL 的主体，全部为载体限制。默认构建路径（PIE/c-shared）不受影响。

### 4. `hdc shell` 传不了 NUL

设备侧 stdio 的载体限制，影响部分测试输出比对，不影响正常产物。

### 5. 真机不能跑设备侧测试

商用真机（`4VM0125513000074`）的 `sh` 域不能 exec `/data/local/tmp` 下的未签名二进制
（HMMAC，`Permission denied`）。本轮所有设备侧结论出自模拟器。真机上跑 Go 的现实路径是
HAP 内加载 c-shared 库（下游 v2rayHM 即此形态，已在真机长稳验证过 30 分钟——用 v1.27.1 工具链）。

### 6. 不要在设备上跑 `runtime/TestTracebackSystem`

该测试把构建机绝对路径烧进测试二进制，镜像 GOROOT 救不了。B 层 22 条 FAIL 里有它。

### 7. 覆盖度的坦白

- `amd64` 目标运行期零证据（无设备）。
- 16KB 页设备未实测（无硬件）；39 位 VMA 结论全部出自 4KB 页模拟器。
- 环回类限制（B 层 22 条 FAIL 的主体）是 **`sh` 权限域**的载体限制，不是 port 缺陷——
  已用应用域 HAP 夹具（`loopbackhap`）定案：同一台模拟器，应用域 `bind 127.0.0.1` 成功。
  但这同时意味着：**通过 `hdc shell` 直接跑的测试看不到环回行为**，应用域的网络行为
  由下游真实项目覆盖。

## 与上一代（v1.27.1-ohos）的差异

- 基线 go1.27.1 → **go1.27.2**（安全加固为主，见上）；
- `net` 接口表三处修复**首次进入发布资产**；
- C 层总量 RUN 2739 → 2744（上游新增 5 用例全 PASS）；FAIL 132 名单与上轮逐条相同，零新增；
- 已知问题清单与上一版相同，无新增。

## 反馈

问题请到 <https://github.com/sysit/ohos-go> 提 issue；下游（v2rayHM）相关问题请附
`go version` 输出与 `CC` 的完整命令行。
