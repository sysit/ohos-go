# OHOS Go 工具链 v1.27.1-beta1

**这是 `golang/go` 的 fork**，把 **OpenHarmony (OHOS)** 加成受支持的 `GOOS`。
基线是上游 **go1.27.1**（`VERSION` 文件是权威），OHOS delta 叠在其上。

**beta 的含义**：目标平台（`openharmony/arm64`）在真实下游项目上编过、跑过、验过；
但覆盖面仍是「已知边界内可用」，边界在下面的「已知问题」里逐条写明。
**edge 掉在边界外时表现为一段看不懂的链接器/加载器报错**，而不是明确的功能缺失。

## 拿到

预编译树（**免 `./make.bash`**）：`go1.27.1-ohos-beta1-darwin-arm64.tar.gz`（167 MB）+ `.sha256`。

```bash
shasum -a 256 -c go1.27.1-ohos-beta1-darwin-arm64.tar.gz.sha256
cd ~ && tar xzf /path/to/go1.27.1-ohos-beta1-darwin-arm64.tar.gz   # → ~/go1.27.1-ohos
export PATH="$HOME/go1.27.1-ohos/bin:$PATH"                        # 是功能开关，见下
```

**先读 `docs/ohos-toolchain-install.md`（在树里）** —— 三条硬前提（`bin` 上 `PATH`、`CC` 写
SDK clang 绝对路径、`go.env` 的 `GOTOOLCHAIN=local` 不要改）各对应一个**指向错误方向**的报错，
不知道就查不出来。

## 支持矩阵

| | |
|---|---|
| 宿主 | **darwin/arm64 唯一**（本 beta 只实测过这一种） |
| 目标 | `openharmony/amd64`、`openharmony/arm64` |
| 设备侧执行 | 模拟器实测；商用真机的 `sh` 域不能 exec 未签名二进制 |

## 验证到什么程度

三层各自独立，**结论不互相背书**（这是本项目反复吃过的教训）：

| 层 | 覆盖什么 | 结果 |
|---|---|---|
| **A** 宿主编译器自测 | 编译器/链接器在宿主上的正确性 | `./all.bash` 绿（本轮未重跑全量，改动范围内 scoped 跑） |
| **B** 交叉编译 + 设备执行 | 262 个 std 包能不能编、能不能在设备上跑 | **239 PASS / 23 FAIL / 0 TIMEOUT / 0 WRAPPER** |
| **C** `test/` 编译器测试集 | 2734 个编译/链接/运行用例 | **2739 RUN / 132 FAIL** |
| **端到端** | 真实下游项目构建 | `v2rayHM/scripts/build_libxray_ohos.sh` 退出 0，产物过全部检查 |

**B 层 23 条 FAIL 全部落已知类**：环回类 17 包、unix socket/SCM_RIGHTS、设备权限模型、
设备无 C 编译器（`runtime` 的 9 个 cgo 测试）、上游 testdata 缺口（`go/internal/gccgoimporter`），
外加 `runtime/TestTracebackSystem`。

**C 层 132 条里有 125 条是同一个链接器致命错误**，而它是 **testdir 测试载体的限制，不是 port 缺陷**：
testdir 的 `rundir` 系列自己调 `go tool link` 且**不传 `-buildmode`**，链接器于是取 `exe`（非 PIE），
而 OHOS 的运行时是 iscgo 构建、stdlib 里带 `R_ARM64_TLS_IE` —— 非 PIE 的内部链接没实现这条重定位。
**用户走 cmd/go 永远撞不到**（`platform.DefaultPIE` 对 openharmony 为 true，默认就是 PIE）。
证据链和剩下 7 条的逐条定性见 `docs/ohos-full-test-plan.md` §5.7。

**端到端证据**（`build_libxray_ohos.sh`，libxray v26.9.9 + SSR 插件）：

- 工具链：`go version go1.27.1 darwin/arm64`；`GOOS=openharmony`、`-buildmode=c-shared`、`-trimpath=true`
- 产物 35978936 字节，与仓库里已提交的 `prebuilt/arm64-v8a/libxray.so` 逐项属性一致
  （ELF 头只差 section-header offset，导出集完全相同）
- `nm -D` 导出集恰为 `CGoFree` + `CGoInvoke`
- `PT_TLS` 段、`R_AARCH64_TLSDESC` 重定位、`GOOS=openharmony` 字符串均在
- v2rayHM 仓库 `git status --porcelain` 为空（脚本只写自己 gitignore 的 `build/`）

## 能力表（每条都有设备上的实测证据）

| 能力 | 结论 |
|---|---|
| cgo 可执行文件 | ✅ **默认即可**，不用加 `-buildmode=pie` |
| `-buildmode=c-archive` | ✅ 但**必须** `AR=$SDK/native/llvm/bin/llvm-ar`（见下） |
| `-buildmode=c-shared` | ✅ `dlopen(RTLD_NOW)`+`dlsym` 与 `-l<name>` 两条路都通 |
| `-buildmode=plugin` | ✅ |
| `-buildmode=shared` | ✅ |
| `-asan` | ✅ |
| **`-race`** | ❌ **不可用**，见下 |
| `-msan` | ❌ 不可行（SDK 无 `libclang_rt.msan*`，且 MSan 要插桩过的 libc） |
| SVE 汇编 | ✅ 需 `GOEXPERIMENT=simd`（编码逐字节相符；硬件执行未验） |
| 应用域内 bind/listen 127.0.0.1 | ✅（**`sh` 域测不出来**，用 HAP 夹具在应用域验的） |
| x509 系统根 | ❌ 取不到，**用 `SSL_CERT_FILE`** |

完整表格、每条的证据串、以及背后的机制在树里的 `docs/go-upgrade-guide.md` **§4.2**。

## 已知问题

**排在前面的更可能咬到你。**

### 1. `-race` 不可用（不开门）

TSAN 的 arm64 `InitializePlatformEarly` 硬要求 48 位 VMA，而设备用户地址空间只有 **39 位**
（ASan 自报 `HighMem [0x002000000000, 0x007fffffffff]`，探针 `vma_bits_stack=38`）。
开了只会把构建期一句清楚的报错换成运行期 sanitizer 崩溃，所以**明确拒绝**。
真机与模拟器都是 39 位。**这是平台物理限制，不是待办。**

### 2. x509 取不到系统根

`certpool: error: open /etc/ssl/certs: permission denied`（目录在，但进程无权限）。
**正解是 `SSL_CERT_FILE` / `SSL_CERT_DIR`**（`root.go` 已尊重）。所以没有加
`root_openharmony.go`，这块**零 delta**。

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

它 `open` 的是**编译期绝对路径**（`/Users/xiphis/projects/ohos-go/src/runtime/...`），
镜像整棵 GOROOT 也救不了（除非 `-trimpath` 构建）。登记为已知不可跑。

### 7. `go/internal/gccgoimporter` 的 testdata 缺口

`testdata/importsar.gox` 在**上游 HEAD 里就不存在**，在宿主上跑同一条 `TestGoxImporter` 也失败。
**不是 port 缺陷**，要修得往上游提。

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
- 已知问题里，`-race` / `-msan` / x509 / 真机 exec 四条与上一代相同（都是平台限制）

## 反馈

问题提到本仓库的 issue。**下游项目（v2rayHM / sing-box / xray）本身的问题不要提到这里** ——
那些提到各自的仓库。
