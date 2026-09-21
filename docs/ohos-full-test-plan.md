# OHOS Go 工具链全量测试方案（go1.27.1）

目标：回答「1.27.1 在 OHOS 上功能是否全量可用」，而不是「几个关键包能不能跑」。
状态：**阶段 0 完成（2026-09-21）**，包装的 cwd/testdata 缺口已修并在设备上实测兑现（§7 阶段 0.4）。落地脚本见第 7 节的分阶段计划。

---

## 1. 边界：先说清楚「全量」做不到什么

先说结论，免得方案写完才发现期待错位。

- **真机跑不了。** 商用版真机 `4VM0125513000074` 的 shell 域不能 exec `/data/local/tmp` 下的未签名二进制（§4.2）。设备侧结论**只能**来自模拟器 `127.0.0.1:5555`。
  （已核对：模拟器 `uname -m` = `aarch64`，与 `GOARCH=arm64` 一致；`/data/local/tmp` 可读写。**真机不在线对本方案零影响** —— 唯一能 exec 的设备就是这台模拟器，别为凑真机花时间。）
- **`-race` 开门即崩。** TSAN 硬要求 48 位 VMA，设备只有 39 位。所有 `-race` 变体不是在"验证功能"，是在验证一个已知不行的事实。
- **单设备串行是硬约束。** `go_openharmony_exec` 用 `flock` 把所有 hdc 访问串行化（`lock()`，因为 hdc 本身不安全并发），而 `go test` 默认并行跑包。所以 262 个包实际是**顺序** push + run，吞吐上不去。多设备并行也不成立——只有一台能 exec。
- **因此全量一轮是小时级，不是分钟级。** 估算见第 6 节。方案必须设计成**可中断、可续跑、可分批**，不能指望一把梭跑完。

---

## 2. 已有的基础设施（复用，不要重建）

| 件 | 位置 | 作用 |
|---|---|---|
| `-exec` 包装 | `misc/go_openharmony_exec` | 被 `go test` / `go run` 经 `go_<GOOS>_<GOARCH>_exec` 约定自动发现，push 到设备执行并回传真实退出码。**并把整棵宿主 GOROOT 镜像到设备、在设备上装一份原生 `go`、从 GOROOT 里对应包的目录运行**（§5.6） |
| 回程解析回归 | `misc/go_openharmony_exec/exitcode_test.go` | 7 例表驱动，防「设备没回传状态被当成成功」；已注册进 dist（`misc:execwrapper`），`./all.bash` 会跑 |
| 镜像/推送回归 | `misc/go_openharmony_exec/runmain_test.go` | 用假 hdc 驱动整个 `runMain`，断言「源文件树推出去了」+「从镜像里 `cd` 进去跑」（两条断言均做过变异验证：改坏哪条哪条红）；另一例覆盖 hdc「打 `[Fail]` 但 exit 0」 |
| `test/` 驱动器 | `src/cmd/internal/testdir` | `-target goos/goarch` 交叉编译；`findExecCmd` 已按同一约定自动找包装 |
| 能力夹具 | `misc/openharmony/`（9 个目录） | 每种 buildmode 一个夹具 + `probe` 事实采集器 + `ohosrun` 推送壳 |
| 推送壳 | `misc/openharmony/ohosrun` | 不经 `go test` 时手工推送执行 |
| **阶段 2/3 驱动器** | `misc/openharmony/runtests.sh` | 上面三个硬前提（`bin/go`、SDK clang 绝对路径、`$GOROOT/bin` 上 PATH）脚本自己设好，照抄必踩的三条不用记。一个包一次 `go test`，配主机侧看门狗 + 每包独立日志，结论追加 `results.tsv`，重跑自动跳过已 PASS。`--all` = B 层 262 包；`--list` 只看清单 |

**几个必须知道的机制细节：**

- **【硬前提】设备测试一律用仓库自带的 `./bin/go`，不要用 `~/go1.27.1-ohos`。** 后者是 2026-09-19 20:29 构建的**旧树，早于 `2417e0834a9`**：它的 `DefaultPIE` 少了 `openharmony`，产出非 PIE 二进制，凡是需要外部链接（cgo）的测试二进制一上设备就在 `init()` 前 `Signal 11`（0.15 s 即挂）。仓库 `bin/go`（21:08）是对的 —— 实测 `e_type=3`（ET_DYN）+ 解释器 `/lib/ld-musl-aarch64.so.1`。**`~/go1.27.1-ohos` 同时是下游 v2rayHM 的 `OHOS_GO_ROOT`，它过期意味着下游会编出 Signal 11 的产物（见 §9 风险）。**
- **【硬前提】`CC` 必须写 SDK clang 的绝对路径。** 裸 `clang` 会解析到 `/usr/bin/clang`（Apple clang），它不认 `aarch64-linux-ohos`，报 `posix_spawn failed: No such file or directory`。**这个文档缺口 2026-09-21 已补齐**：`CLAUDE.md`、merge skill §6、`docs/go-upgrade-guide.md` 的能力验收夹具一节现在都写绝对路径（原先只有 `runtests.sh` 是；照抄旧示例会中招，因为 SDK clang 通常不在 PATH 最前）：
  ```bash
  CC="$SDK/native/llvm/bin/clang --target=aarch64-linux-ohos --sysroot=$SDK/native/sysroot -D__MUSL__"
  ```
  只走内部链接的纯 Go 包不需要 clang，所以这个错**只在一部分包上暴露** —— 这正是它容易被漏掉的原因。

- **包装要手装进 `$GOROOT/bin`**，且普通 `make.bash` 不会装它（`wrapperPathFor` 只在 `GOOS=openharmony` 交叉自举时才命中）：
  ```bash
  cd misc && $GOROOT/bin/go build -o $GOROOT/bin/go_openharmony_arm64_exec ./go_openharmony_exec
  cp $GOROOT/bin/go_openharmony_arm64_exec $GOROOT/bin/go_openharmony_amd64_exec
  ```
- **`$GOROOT/bin` 必须在 `PATH` 上**（`export PATH="$GOROOT/bin:$PATH"`），否则 `pathcache.LookPath` 找不到包装（`cmd/go/internal/work/build.go:909`），症状是 `fork/exec …/pkg.test: exec format error` —— 宿主直接去 exec 交叉二进制了。它**只查 PATH，没有 `$GOROOT/bin` 兜底**（`src/cmd/internal/pathcache/lookpath.go` 就是裸 `exec.LookPath`）。
- **包装把整棵 GOROOT 同步到设备，并在里面跑**（2026-09-21，取代原先「只镜像包目录」的做法；实测结果见 §5.6）。同步 = 手装设备原生 `go`（`GOOS=openharmony GOARCH=arm64 CGO_ENABLED=0 go install cmd` → `goroot/bin/go` + 174 MB 的 `pkg/tool/<target>/`）→ tar+gzip **`src`/`lib`/`test`/`VERSION`**（`mirrorEntries()` 的清单）→ 一次 `tar xzf` 落到 `/data/local/tmp/go_openharmony_exec/goroot`（364 MB / **12020 个源文件，与宿主逐一点过数**）。**为什么不逐文件 push**：hdc 的递归 `file send` 按**文件数**收费 —— 实测 6 ms/文件 × 12020 ≈ 75 s；单文件却跑到 **406 MB/s**。tar 一次 364 MB 只要 ~11 s。指纹（`mirrorEntries()` 列出的每一项的 sha256，即 `src`/`lib`/`test`/`VERSION`）缓存在宿主 `/tmp`、**文件名带 `<goos>_<goarch>` 后缀**（多设备共存时缺了它会把别人的树当成自己的），命中就跳过同步，所以续跑时每包只多 ~0.3 s 的走树。
- **设备执行环境由包装下发**（`deviceEnv`）：`GOROOT`/`PATH`/`TMPDIR`/`GOCACHE`/`GOPATH`/`HOME`/`GOTOOLCHAIN=local`/`GOPROXY`，外加 `forwardedEnv` 白名单（`GODEBUG`、`GOGC`、`GOMEMLIMIT`、`GOMAXPROCS`、`GOTRACEBACK`、`SSL_CERT_FILE`）。**`GOOS`/`GOARCH`/`CGO_ENABLED` 故意不转发**：前两个由设备原生 `go` 自己定；`CGO_ENABLED` 一旦下发就会改掉**测试进程自己**的 `build.Default`（`go/build/build.go:360` 读 `os.Getenv("CGO_ENABLED")`），而设备本来就报 0（它自己检测不到 C 编译器），下发的只会让环境与构建配置（宿主一律 `CGO_ENABLED=1`）不一致。android 版转发 `CGO_ENABLED=0` 是因为它的交叉编译默认就是 0，前提不同。
- **cwd 从镜像里推**：包的源文件树就在 GOROOT 里的 `src/<import path>`，包装直接 `cd` 进去；只有**不在 GOROOT 里**的包（外部模块）才回落旧的 `pushSourceTree`。同步失败时 `syncGoroot` 返回空串，包装**退化成改动前的行为** —— 没有 GOROOT 的宿主照样能跑不需要源码树的包。
- **`go run` 也走这套**，所以 `test/` 里的 `// run` 用例理论上会自动在设备上执行 —— **但这一条尚未验证**，是本方案要确认的第一个未知数（见 3.C）。

---

## 3. 三层设计（关键：三层不能混为一谈）

用户说的「所有 `*_test.go`」实际横跨三个性质完全不同的层次。混在一起做会得出错误结论。

### A 层 —— 工具链自测（跑在 macOS 宿主上）

`cmd/...`、`internal/...` 里验证**编译器和链接器**的测试。编译器本身就是宿主程序，`GOOS` 只影响它处理的目标，**交叉编译这些测试没有意义**。

- 载体：`./all.bash` 或 `go tool dist test`
- 注意：`dist test` 是宿主导向的（`runOnHost` 会强制把 GOOS 拉回宿主，`test.go:489/512`），**不能**用来跑设备测试
- 这一层是**回归基线**：OHOS delta 改了 `obj/x86`、`obj/arm64`、`ld`、`platform`，A 层必须全绿

### B 层 —— 交叉编译 + 设备执行（本方案的主体）

`runtime` + `stdlib` 在真机（模拟器）上的行为。这才是「1.27.1 在 OHOS 上能不能用」的答案。

- 载体：`GOOS=openharmony GOARCH=arm64 go test -exec=... std`（包装靠约定自动发现，不必显式写 `-exec`）
- 规模（实测，`CGO_ENABLED=1`）：`go list std` = **381 包**，其中有测试的 **262 包**
- **cgo 开关会改变包集合，量数必须锁定一种配置。** 本方案一律用 `CGO_ENABLED=1`（cgo 才是这个 port 的主战场，musl TLS 整条链路都在里面）。作为对照：`CGO_ENABLED=0` 时 std 少两个包 —— `runtime/cgo` 与 `internal/runtime/cgobench` —— 总数 379，有测试的 260。**任何写「379」或「260」的旧记录都是 cgo 关的数，别用。**

### C 层 —— `test/` 目录（编译器正确性，2734 例）

| action | 例数 | 需要设备执行？ |
|---|---|---|
| `// run` | 1068 | ✅ |
| `// errorcheck` | 668 | ❌ 只编译 |
| `// compile` | 527 | ❌ |
| `// compiledir` | 121 | ❌ |
| `// rundir` | 114 | ✅ |
| `// asmcheck` | 87 | ❌ |
| `// build` | 35 | ❌ |
| `// runoutput` | 22 | ✅ |
| `// runindir` | 11 | ✅ |
| `// buildrundir` / `// builddir` / `// buildrun` | 9 | 部分 |

即 **~1400 例只编译**（很快，可全量跑），**~1220 例要设备执行**（慢，是耗时大头）。实际数量会被 build tag 的 `shouldTest` 再砍掉一批。

- 载体：`go test cmd/internal/testdir -target=openharmony/arm64`
- **已验（阶段 0.1）**：`-target` 下 `// run` 确实经包装落到设备执行。附录证据见第 7 节阶段 0 的第 1 项。

---

## 4. 目标集怎么定：用 `go list` 推导，不手写清单

用户说「挑与我们平台架构相关的」。**不要手维护这个清单** —— 任何手写列表都会漏，且随上游漂移。

正确做法：让工具链自己回答「在 `openharmony/arm64` 上，哪些包会被编译且有测试」：

```bash
GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1 \
  go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' std
```

这返回的就是全集，**不需要「挑」**。真正的过滤只发生在**运行期**（skip 名单），不在选包期 —— 因为「这个包的平台相关测试」和「这个包」不是一回事：一个包可能有 30 个测试，其中 28 个在设备上能过。

所以：**选包用推导，排除用名单**，名单每条必须带原因（沿用 §4.2 的写法）。

---

## 5. 已知不可跑清单（v1，待实测增补）

现有证据只支持列这几条；其余要靠第 7 节的 triage 阶段补全。

| 类别 | 处理 | 依据 |
|---|---|---|
| **依赖 cwd 的测试** | **已修**（阶段 0.4） | 原缺口：包装不设工作目录，设备上 `hdc shell` 的 cwd 就是 `/`（实测 `pwd` = `/`）。修前实例：`io/fs` 的 `TestGlob` 把根目录当测试目录，`os` 在 cwd 找 `os_test.go`/`stat_linux.go` → `could not find …`。修法：`pushSourceTree` 把包目录镜像到 `<work>/cwd/<宿主绝对路径>` 并 `cd` 进去 |
| **依赖 `testdata/` 的测试** | **已修**（阶段 0.4） | 原缺口：包装不推送包的源文件树（修前实例：`text/template` 的 `TestParseFiles` → `open testdata/file1.tmpl: no such file`）。hdc 的 `file send` 递归拷目录，所以一次调用把源码和 `testdata/` 一起带过去 |
| **需要 `$GOROOT` 落地的测试** | **阶段 2 实测已确认**，两个包 | 包装**不像** `go_android_exec` 那样把 GOROOT 拷到设备（它 527 行 vs 我们 264 行，少了 `adbCopyGoroot`/`adbCopyTree`/`pkgPath`）。实测命中 `time`（下详）。**注意这一类里有两种，可修性完全不同**，见下方「源文件可用性」 |
| **任何依赖环境变量的测试** | **预期必挂** | **包装不下发 env**：`run()` 里 `cmd.Env` 未设，命令行只有二进制路径（`exec.Command(hdcPath, append(hdcArgs(target), "shell", cmdline+"; echo "+exitStr+"$?")...)`）。宿主 env 到不了设备进程。**这条推翻了本表原本对 `crypto/x509` 的处方** —— 「设 `SSL_CERT_FILE` 就好」是错的，宿主设了没用，得改包装 |
| `-race` 变体 | 不开门 | 39 位 VMA vs TSAN 要求 48 位（§4.2） |
| `-msan` 变体 | 不开门 | SDK 无 `libclang_rt.msan*`，且 MSan 要求插桩 libc（§4.2） |
| `crypto/x509` 系统根 | 待定，需改包装 | 设备上 `/etc/ssl/certs` 对 shell uid 是 EACCES，且 env 不下发（见上） |
| **环回 TCP listen 被禁** | **预期必挂，整类** | 模拟器 shell 域不许 `net.Listen("tcp4","127.0.0.1:0")` 也不许 tcp6。`nettest.probeStack`（`src/vendor/golang.org/x/net/nettest/nettest.go:37`）两个探针都失败 → 报 `tcp is not supported on linux/arm64`（措辞里的 `linux` 正是 split identity 的直接体现）。已见实例：`os` 的 `TestSendFile/sendfile-to-tcp/*`。**凡需要本地监听端口的包（`net`、`net/http`、`crypto/tls`）都会撞上**，这是除权限之外最大的一类环境噪音 |
| 设备权限模型 | 按失败记录 | 模拟器 shell uid 受限。已见：`/etc` 读不了（`TestFileReaddir*`、`TestDoubleCloseError/dir`）、`lchown` 被拒（`TestLchown`）、`link()` 被拒（`TestLongPath`）、fifo `open` 被拒（`TestFifoEOF`）、`/dev/null` 与 `/dev/stdin` 的 `stat` 被拒（`TestDevNullFile`、`TestStatStdin`） |
| `net` 系 DNS/网络 | 未知，先跑 | `probe` 采集了 DNS 事实，但未做过整包测试 |
| **内存吃爆 guest → 整轮陪葬** | **必须开 `-short`**，见 §5.2 | 2026-09-21 实测：`archive/zip` 的 `TestZip64LargeDirectory` 峰值 RSS **~16GB**，把 4GB guest 打成 `Out of memory and no killable processes` → **kernel panic**。**后果不是那个包 FAIL，是模拟器卡死、整轮作废** |
| SVE 硬件执行 | 不适用 | 只有汇编器验证（§4.2）；shell uid 读不了 `/proc/cpuinfo` |

**前两类已经修掉了**（阶段 0.4），它们本来就不是平台限制，只是包装缺了 android 版的那几件事。**env 那一类仍未修** —— 剩下的 harness 噪音主要来自它，加上上面「环回 TCP」和「权限模型」两类**真·环境**限制（那两类修不了，只能登记）。

分界要守住：**FAIL 集合里「harness 缺口」和「环境限制」必须和「port 缺陷」分开标注**，否则信号被淹没。这是本方案最大的单一风险，见 §9。

### 5.1 阶段 2 实测结果（2026-09-21，模拟器 `127.0.0.1:5555`）

13 个核心包，`runtests.sh -v`：**6 PASS / 7 FAIL / 0 TIMEOUT / 0 WRAPPER** —— harness 本身健康（没有一例是设备没回传状态），所以 7 个 FAIL 全是真信号。逐条 triage 后 **没有一例是 port 缺陷**，全部落进下面五类环境限制 + 一类 harness 天花板：

| 类 | 包 | 决定性证据 |
|---|---|---|
| 环回 TCP/UDP 被禁 | `net`(61 FAIL)、`net/http`(3)、`os/exec`(1)、`os` | `listen tcp6 [::1]:0: bind: permission denied`、`dial udp …: setsockopt: permission denied`、`http_test.go:128: GOROOT/src not available`（此条是 SKIP，非 FAIL） |
| unix socket 被禁 | `net`、`syscall`(2) | `listen unix …/sock: bind: permission denied`、`creds_test.go:55: getsockopt: permission denied`、`TestPassFD: child process: "failed to find unix fd"` |
| `linkat`/`link` 被拒 | `os`(TestHardLink、TestRootLinkFrom/RenameFrom) | `root.Link("a","destination") = linkat a destination: permission denied; want success` |
| fifo `open` 被拒 | `os`(TestFIFONonBlockingEOF) | `fifo_test.go:217: Error opening fifo for read: open …/issue-66239-fifo: permission denied` |
| `prlimit`/rlimit 被拒 | `syscall`(TestPrlimitOtherProcess) | `Failed to get the current nofile limit: permission denied`（**同文件的子测试 `TestPrlimitFileLimit` PASS**，说明测的是权限不是功能） |

`net` 的 DNS 类失败（19 条 `error message is not equal to: no such host`）也归第一类：设备上 DNS 走不通的**报错文本**变成了 `setsockopt: permission denied`，测试断言的是文本。**不是 DNS 实现有缺陷。**

**新发现的一类：源文件可用性（harness 天花板）。** `time` 与 `runtime` 各命中一种，**可修性截然不同**，不能混为一谈：

| 包 | 症状 | 性质 |
|---|---|---|
| `time` | `panic: cannot load America/Los_Angeles …` | **可修，且 §5.6 已修**。`initTestingZone()` 为求 hermetic **只用** `../../lib/time/zoneinfo.zip`（相对包目录）。镜像只推了包目录本身，`../../lib` 在设备上不存在。android 版靠 `adbCopyTree` 向上走解决 —— 即 `pushSourceTree` 那条 `ponytail:` 天花板。**镜像后该 panic 消失**，`time` 剩下的 FAIL 换成了另一条（`TestEnvTZUsage`：设备 `/usr/share/zoneinfo` 是 EACCES，测试的 `os.IsNotExist` 守卫不成立），归「设备权限模型」类 |
| `runtime` | `panic: open /Users/xiphis/projects/ohos-go/src/runtime/traceback_system_test.go: no such file or directory` | **不可修（除非 `-trimpath`）**。`formatStack` 拿到的是**编译期绝对路径**，直接当绝对路径 `open`。设备上要存在 `/Users/xiphis/…` 只能靠 root 建符号链接。**镜像 GOROOT 也救不了它**（§5.6 实测仍在），这一条登记为已知不可跑，不要试图修包装 |

**结论：阶段 2 全绿不了，但「绿不了」的原因全部有据。** 真正的判据是 §8 第 2 条 —— FAIL 集合 ⊆ 本表，本轮**做到了**。

### 5.2 内存吃爆 guest：唯一一类「不是 FAIL，是把整轮搞死」的失败（2026-09-21）

全量轮前三次尝试**全部**在 `archive/zip` 上把模拟器打死，签名完全一致：

```
[   3460]  2000  3460  1314593   844378 ... zip.test
Out of memory and no killable processes...
Kernel panic - not syncing: System is deadlocked on memory
```

排查路径（**每一步都推翻了上一个假设，记下来免得重走**）：

| 假设 | 结果 |
|---|---|
| 模拟器老化漂移（那台已连续跑 16 天） | **否**。重启后全新实例照样死 |
| 冷启动时跑测试撞上开机内存高峰 | **部分成立但不是主因**。加「等进程数收敛」后仍死，panic 发生在 guest uptime **341 秒**（远在开机之后） |
| port 有内存缺陷（OHOS 上 Go 二进制吃内存异常） | **否**。把 `archive/zip` 的测试二进制用**宿主工具链**编成 darwin/arm64 在 Mac 上跑，峰值同样 **~16GB**（`time -l` 17.3GB、`ps -o rss` 实时采样 15.5GB，两种方法互证）。**跟 OHOS 无关，是测试代码自身的** |
| 推送的源文件树撑爆了（`pushSourceTree` 推整个包目录） | **否**。`src/archive/zip` 只有 476K |

**真凶**：`TestZip64LargeDirectory/uint32max_HasZip64` —— 它为验证 zip64 边界，用 `rleBuffer` 造出 ~4GB 的逻辑中央目录。

**处置：默认加 `-short`。** 该测试自己写了 `testing.Short()` 跳过（Go 官方在受限环境也是这个做法），实测 `-short` 后该包峰值从 16GB 掉到可忽略、整包 PASS（跳过 7 个用例）。**这不是覆盖率上的妥协，是 4GB guest 物理上跑不动的那些用例，本就自带跳过开关。**

**待办**：`-short` 只覆盖「自觉声明了 `Short()`」的测试。**没有声明却吃内存的用例仍会打死设备** —— 全量轮若再出现同类 panic，按 panic dump 里的进程名定位，登记进本表。

**附：`WRAPPER` 这一态在真实故障里兑现了一次。** 同日启动全量轮，设备在中途掉线，**已跑的 112 个包全部被标成 `WRAPPER`，没有一个被算成 PASS**（日志是 `hdc` 的 `[Fail][E001005] Device not found or connected`）。这正是 §8 那条「不允许『设备没回传状态』被算成 PASS」要防的情形 —— 它在 `480afb8fc47` 里是个真 bug，现在被拦住了。**如果当时是按退出码判**，`go test` 会把包装的 125 压成 1，这 112 个包会整片落进 `FAIL`，混进上面那五类环境限制里 —— **信号就淹了**。这也是为什么 `WRAPPER` 必须靠日志前缀认，不能靠退出码。

### 5.3 阶段 3 全量实测结果（2026-09-21，`--all`，262 个包）

> **这一节是修复前的基线（v1，包装还没做 GOROOT 镜像）。** 后续修复的效果、以及本节的哪几行已被消掉，见 **§5.6**。

`runtests.sh --all -v -t 600 -T 480` 跑完，`-short` 生效后**设备全程存活**（对比 §5.2 的三次陪葬）：

| 状态 | 数 |
|---|---|
| PASS | **226** |
| FAIL | **36** |
| TIMEOUT | **0** |
| WRAPPER | **0** |

口径说明：`results.tsv` **跨轮追加**（同一包多行 = 中途补跑过），上表取**每包末次**；原始行数是 264 行 / 38 条 FAIL 行，差的 2 条是 `internal/zstd`（FAIL→PASS）和 `runtime`（FAIL→FAIL，两行同包）。**引用这些数字时别直接 `wc -l`**，会得到 264/38 这种口径不同的值。

TIMEOUT/WRAPPER 双零 = harness 侧没有噪音，**36 个 FAIL 全是真信号**。逐条 triage 后：

**36 条全部落进已知类，没有一条是 port 缺陷。** 已知类的分布：

| 类 | 包数 | 代表包 | 决定性证据 |
|---|---|---|---|
| 环回 TCP/UDP 被禁 | 19 | `net`、`net/http`、`net/rpc`、`net/smtp`、`log/syslog`、`encoding/json`、`context`、`net/http/{cgi,httptest,httputil,cookiejar,pprof,internal/http2}`、`compress/gzip`、`crypto/tls` | `listen tcp4 127.0.0.1:0: bind: permission denied` —— **v4/v6 无差别**：`listen tcp4 127.0.0.1:0` 全轮出现 **96 次**、`net.log` 里 `bind: permission denied` **167 条**，所以这不是 v6 假象（§5.1 曾只引 `tcp6 [::1]` 一条，证据串偏弱，此处补强）。`crypto/tls` 与 `net/http/pprof` 是 `init()` 里就 listen，连一个测试都没跑到，日志只有 3 行 / 13 行 |
| unix socket / SCM_RIGHTS | 4 | `syscall`(TestSCMCredentials/TestPassFD)、`os/exec`(TestExtraFilesRace) | `getsockopt: permission denied`、`failed to find unix fd` |
| 设备权限模型 | 3 | `os`(19 个)、`crypto/x509`、`syscall` | `/etc/ssl/certs: permission denied`、`linkat …: permission denied`、fifo `open`、`TestDevNullFile`/`TestStatStdin` |
| 设备上没有 `go` / 宿主源码树 | 8 | `go/build`、`go/types`、`go/parser`、`internal/copyright`、`math/big/internal/asmgen`、`path/filepath`、`runtime`、`crypto` | `lstat /Users/xiphis/projects/ohos-go/src/unicode: no such file or directory`、`'go build' unavailable: exec: "go": executable file not found in $PATH`、`../../arith_386.s`、`TestDisallowedAssemblyInstructions`（走 `go tool dist`）。原判「不可修」→ **§5.6 已修**（镜像整棵 GOROOT + 装设备原生 `go`） |
| 镜像只覆盖包目录本身 | 3 + 若干 | `time`、`io/ioutil`、`compress/{flate,lzw,zlib}`、`internal/zstd`、`image/{draw,gif,jpeg}` | 路径是 `../testdata/`、`../../testdata/`、`../../lib/time/zoneinfo.zip`、`..`。**方向是「向上或向旁」**，`pushSourceTree` 只推包目录自身。与 §5.1 的 `time` 同一根因 → **§5.6 已修** |

**⚠️ 更正（同日复查）：「环回被禁」这一类的定性存疑，别当成平台天花板。**

`bind(127.0.0.1:0)` 在 Linux 语义下**不需要任何特权**，拿到 `EACCES` 说明是**按域施加的策略**（seccomp / LSM 一类沙箱规则），不是内核能力不足。而 OHOS 上的**应用进程必须能听环回** —— 下游 v2rayHM / sing-box / xray 的存在前提就是应用进程里起一个 127.0.0.1 本地代理。两条合起来指向同一个结论：**是 `hdc shell` 那个受限域不让听，不是平台不让听。**

- 已做的排除：设备 `/proc/net/tcp{,6}` 当前 4 个 LISTEN **全部在 `10.0.2.15`**（模拟器 NAT 地址），没有一个是 `127.0.0.1`。这个探针**既没证实也没证伪**，只排除了「设备上另有进程在听环回」这个反例。
- 要落地证实：让一个**应用进程**（HAP，或最小化地把 binder 塞进 HAP）去 bind 环回。那是换测试承载方式，不是改 `-exec` 包装。
- 影响：这 19 包的 FAIL **不能**记成「平台做不到」，只能记成「我们这种启动方式做不到」。判据据此收紧 —— 它们仍是**未定性的**，不是已解释的。

`internal/copyright` 值得单记一笔：它 `filepath.WalkDir(GOROOT/src)`，walk 在设备上必然报错，而回调里**没判 `err` 就对 `d` 取 `d.IsDir()`** → nil 解引用 panic。这是上游测试的健壮性问题，但触发条件仍是「GOROOT 不在设备上」，归上一类（§5.6 修掉镜像后本包即 PASS）。

**⚠️ 勘误：本轮的真 port 缺陷（FIPS，§5.4）不在这 36 条里。** `crypto/internal/fips140only` 在全量轮**一直是 PASS** —— 缺陷只在 `GODEBUG=fips140=only` 下、于**设备上跑一个含 crypto 的自建二进制**时才现形，全量轮既不设该 GODEBUG、也不做这种验证。**这是本方案覆盖不足的实证：sweep 全绿 ≠ 没有 port 缺陷。** 详见 §5.4。

---

### 5.4 FIPS 完整性自检在 OHOS 上静默失效（2026-09-21 定位并修复）

**症状**：`GODEBUG=fips140=only` 下任何含 crypto 的 OHOS 二进制在 `init()` 阶段 panic：

```
panic: fips140: verification mismatch
crypto/internal/fips140/check.init.0()  …/check/check.go:93
```

**这不是「设备环境限制」，是 port 自己的问题** —— 同一棵树、同一份源码，宿主 darwin/arm64 上该测试 `ok`。

**定位过程**（结论都可复现，不必重走）：

1. 二进制里 `.go.fipsinfo` 的 `self` 和 4 个段区间**全是 0**。它们不是丢了 —— `.rela.dyn` 里有 9 条 `R_AARCH64_RELATIVE`，addend 正是那些地址（`0x4989e0`、`0x3c3820..0x418200`…）。**值在重定位表里，文件字节是 0。**
2. `FIPS` 的四个段**自身零重定位**（text/rodata/noptrdata 逐条查过），所以「哈希段内容」这个不变式是成立的 —— 问题只在 `.go.fipsinfo` 的指针本身。
3. `elffips` 的注释写明它**假设 addend 已预存进 section 数据**（"the addend is in the data itself in addition to being in the relocation tables"），并留了一句退路：*"unless we find a toolchain that doesn't initialize the data this way"*。**lld 就是那个工具链** —— 而 port 恰恰在 `lib.go:1727` 为 openharmony 显式选了 lld。
4. 后果：4 个区间读出来都是 `start=end=0`，`for _, prog := range ef.Progs` 的 `prog.Vaddr <= start && start <= end && end <= prog.Vaddr+prog.Filesz` 在 **vaddr=0 的那个 PT_LOAD** 上成立（`0<=0` 恒真），于是**四个段全按长度 0 参与哈希**。写出的 sum 非零、但无意义。
5. 决定性验证：链接器自带的 `-ldflags=-fipso=/tmp/fipso.bin` 倒出的**恰好是 19 字节头 + 四个长度 0 的段标记**，而文件里那个 sum 与「四个空段的 HMAC」**逐字节相同**：

   ```
   四个空段的 HMAC: 5bfb56251f1cb36dde368c0bc98bf6b0ded874dfa5a2d3fab76e326fff6c112a
   文件里的 sum     : 5bfb56251f1cb36dde368c0bc98bf6b0ded874dfa5a2d3fab76e326fff6c112a
   ```

**为什么是静默的**：`hostlinkfips` 的返回值在 `lib.go:2167` 被丢弃，而这条路径**没有返回错误**（它「成功」地哈希了四个空段），所以链接期一个字都不说。运行期则报 `verification mismatch` 而非 `no verification checksum found` —— 后者才对应「sum 没写」。**这个措辞差异是唯一的分诊线索。**

**波及面**：openharmony 强制外部链接（纯 Go 包也调 clang，实测 16 次），`-buildmode=c-shared` 同样中招。**即 port 产出的每一个含 crypto 的产物，FIPS 自检都是坏的** —— 默认构建不受影响（`fips140=only` 是 opt-in），但用 FIPS 模式的人会拿到一个无解 panic。

**修复**（`cmd/link/internal/ld/fips140.go`，新增 `elffipsRelocs`）：`elffips` 读段数据后，把仍为 0 的指针槽按 `.rela.dyn` 的 addend 补回。**addend 就是链接期虚拟地址，正是运行期期望读到的值**，补回即可。已填好的槽不动 → 对上游那种「addend 预存」的链接器是 no-op。

**顺带记一条可能的上游问题**：这不是 OHOS 独有 —— **android 同样用 lld + 外部链接**（`lib.go:1727` 那行的条件是 `android || openharmony`），推测上游 android 的 FIPS 也一样坏。本轮只在 OHOS 上实测，未在 android 上验证。

---

### 5.5 `decodetypeGcprogShlibByReloc`：delta 复验（2026-09-21）

**背景**：这是 port 唯一一处上游没有的链接器 delta（`ld/decodesym.go` + `ld/lib.go` 的 `addendMap`），由 1.26.5 代带过来。按「忠于上游」的要求**从 1.27.1 源码 + 实测重推，而不是沿用**。结论：**它必要且正确**，但本轮修掉了两处缺陷。

**它解决什么**：`-buildmode=shared` 产出的 `libstd.so` 里，type descriptor 的 `gcdata` 字段（`abi.Type` 偏移 32 = `2*PtrSize+8+1*PtrSize`）。该字段只在**主二进制拥有一个「类型定义在 shlib 里且含指针」的数据符号**时才被读到（唯一入口 `GCProg.AddType`，`data.go:1427`）。

**上游的机制为什么覆盖不到**（三组独立证据）：

1. 上游 `decodetypeGcprogShlib` 是**裸字节读**（`decodeInuxi(..., data[32:])`），假定值已在 section 里。上游自己那个 `relocTarget` 只收 `R_*_ABS64` **且 `addend == 0`**（`lib.go:2916` 的 `if addend != 0 { continue }`），并且只服务 `decodeTargetSym`。
2. 实测 `pkg/openharmony_arm64_dynlink/libstd.so`：**53410** 个去重描述符，`V+32` 是 `R_AARCH64_RELATIVE` 的 **53410/53410（100%）**，是 `ABS64` 的 **0 条** → 上游的两个机制在这条路径上**都够不着**。
3. 盘上 `V+32` 字段为 0 的 **49371/53410（92.4%）**；0 条描述符缺 reloc，0 条 `addend == 0`。⇒ 上游裸读会给出 **0**。**gcprog=0 对一个含指针的类型 = GC 永不扫那些槽**（静默的堆损坏）。

**根因与 §5.4 完全同源**：`.comment` 显示 `Linker: LLD 15.0.4` —— **lld 不把 RELATIVE 的 addend 预存进 section，只留在 `.rela.dyn`**。§5.4 是这条性质在 `elffips` 上的表现，本节是它在 `gcdata` 上的表现，同一棵树里两个独立消费者。

**本轮修掉的两处缺陷**：

| 缺陷 | 症状 | 修法 |
|---|---|---|
| **amd64 空洞**（静默错值） | `getRelocAddendMapShlib` 只处理 `EM_AARCH64`；`openharmony/amd64` 的 shlib 链接拿不到 map，**静默回落**到零 gcprog | 泛化为 `getRelocAddendMapShlibELF64(f, libpath, relative uint32)`，覆盖 `R_AARCH64_RELATIVE` / `R_X86_64_RELATIVE`（两侧 `asm.go` 都以 `symNo==0` 发射，lld 亦然）。**仅 arm64 实测**（无 amd64 设备），已留 `ponytail:` 注释 |
| **静默降级** | miss 时 `log.Printf` + 返回 `false` → 回落到一个**已知为 0** 的值 | 改为：map 支持但字段为 0 且类型 `ptrdata != 0` → `Exitf`（带符号名/ptrdata）。机器不支持时仍安静回落 |

这正是 §5.4 的形状：**链接期不报错 ≠ 对**。FIPS 已经证明这条路径上「成功」可以是假的。

**验证**：注入探针重链（`-toolexec` 换 link）→ `PROBE-ENTER`/`PROBE-HIT` 各 4、`PROBE-LOOKUP-MISS` **0**、map 条目 76774 = 独立计数的 RELATIVE 条数；返回的 addend 指向的掩码字节内容正确（`[]string` 得 `01`、无指针类型得 `00`）。`./bin/go test cmd/link/...` 全绿；干净 `-linkshared` 链接不误报 `Exitf`。**注**：`-toolexec` 轮的产物与普通轮 **action ID 相同**（工具 ID 即 `link version go1.27.1`），普通轮是**缓存命中**而在复放探针输出 —— 判据必须用 `-a -x` 强制全量重链，那才是真 0。

**上游面**：与 §5.4 同一句 —— **android 同样 lld + 外部链接**，同样的隐式假定，本轮未在 android 验证。

---

### 5.6 消掉「设备上没有 GOROOT」这一类（2026-09-21）：36 FAIL → 23 FAIL

§5.3 那张表里有两行是**我们自己的承载方式**造成的，不是平台限制 ——「设备上没有 `go` / 宿主源码树」8 包、「镜像只覆盖包目录本身」3+ 包，合起来就是 §5.1/§5.3 反复出现的 `../../testdata/...: no such file`、`'go build' unavailable`、`lstat /Users/xiphis/...`。本节把这两行消掉。

**改动全在 harness 侧，一行测试用例都没动**（`misc/go_openharmony_exec/main.go`，即 OHOS 版的 `go_android_exec`）：包装在推测试二进制之前，先把宿主 GOROOT 的 **`src/ lib/ test/ VERSION`** 打包推到设备、在设备上装一份**原生 `go`**，然后把设备侧的工作目录设成 `GOROOT/src/<importPath>`。上游 `go_android_exec` 的 `adbCopyGoroot` 是同一个思路，这里照抄。

几个必须记的点：

- **打包而不是逐文件推。** hdc 的开销是**按文件**计的：6 ms/文件 × 12020 个文件 ≈ 75 s；而单文件能跑到 406 MB/s。所以 tar+gzip 一次推（364 MB 未压缩 / 12020 文件）。
- **指纹落在宿主 `/tmp`，带 target 后缀**（`go_openharmony_exec-goroot-sync-<goos>_<goarch>`）。不带后缀时第二台设备会被告知「树是最新的」——这个 bug 犯过一次。
- **镜像清单只有一个来源**：`mirrorEntries()` 同时决定「推什么」和「指纹算什么」，避免两边漂移。`pkg/` 特意**不推**（那是 468 MB 的宿主构建缓存），只推其中的 `include` 和 `tool/<target>`。
- **`test/` 和 `VERSION` 是必须的，不是顺手带的**：`go/types` 会 type-check `GOROOT/test/ken`；`cmd/dist`（经 `go tool dist list` 被 `crypto` 和 `internal/platform` 触达）的 `findgoversion()`（`cmd/dist/build.go:373`）**先读 `$GOROOT/VERSION`**，读不到才回落 `git log` —— 而设备上没有 `git`。
- **env 走白名单透传**（`GODEBUG`/`GOGC`/`GOMEMLIMIT`/`GOMAXPROCS`/`GOTRACEBACK`/`SSL_CERT_FILE`），**故意不含 `CGO_ENABLED`**：见下面的新增类 1。上游 android 包装是直接 `export CGO_ENABLED=0` 的，这里没有跟。

**前后对比**（同一台模拟器，`--all`，262 包）：

| | v1（无镜像） | v3（有镜像） |
|---|---|---|
| PASS | **226** | **239** |
| FAIL | **36** | **23** |
| TIMEOUT / WRAPPER | 0 / 0 | 0 / 0 |

**修好 14 个包**（v1 FAIL → v3 PASS）：`compress/flate`、`compress/lzw`、`compress/zlib`、`crypto`、`go/build`、`go/parser`、`go/types`、`image/draw`、`image/gif`、`image/jpeg`、`internal/copyright`、`io/ioutil`、`math/big/internal/asmgen`、`path/filepath`。

**新到 1 个**：`internal/trace`（v1 PASS → v3 FAIL）。**不是新坏，是终于跑到了** —— 它的用例要起 `127.0.0.1` 上的子进程（走设备上的 `go`），v1 里因为没 `go` 直接跳过了。它落「环回被禁」类。

**v2 与 v3 逐包结果逐字节相同** —— 这一节的两个数字来自两次独立全量跑，互为复现证据，不是单次观测。

**顺带暴露的三个新类（都是「终于跑到了」，不是回归）**：

| 类 | 包 | 判据 | 处置 |
|---|---|---|---|
| **设备上没有 C 编译器** | `runtime`（9 个 cgo 测试） | `//go:build cgo` 的文件**在宿主交叉编译时被编进设备二进制**（`runtests.sh` 用 `CGO_ENABLED=1`），但设备 `go build` 出来的 `testprogcgo` 没有 cgo → 二进制打印 `unknown function: …`。`crash_cgo_test.go` 只守卫 `MustHaveGoBuild`，**没守卫 cgo** | **不改**。归类为设备能力限制 |
| **设备上没有 `git`** | `crypto`、`internal/platform`（经 `go tool dist`） | `findgoversion()` 读不到 `$GOROOT/VERSION` 才回落 `git log` | 镜像 `VERSION` 后**已消** |
| **上游测试自身的 testdata 缺口** | `go/internal/gccgoimporter` | `testdata/importsar.gox` 在 HEAD 里就不存在，**宿主上跑同一条 `TestGoxImporter` 也失败**（同一条报错） | **不是 port 缺陷**。要修得往上游提 |

**为什么不跟上游 android 那样直接 `export CGO_ENABLED=0` 把类 1 抹平**：那个变量是**构建配置**，不是环境旋钮。`src/go/build/build.go:360` 读的就是 `os.Getenv("CGO_ENABLED")`，导出去等于翻转**每个测试进程**的 `build.Default.CgoEnabled` —— 把「设备上编不了 C」这条真事实盖住，换来一个假的绿。宁可留 9 个 FAIL 并记成本类。

**残留 23 个 FAIL 的构成**：环回类 17 包（含新到的 `internal/trace`）、unix socket/SCM_RIGHTS（`syscall`）、设备权限模型（`crypto/x509` 的 `/etc/ssl/certs`、`os`、`time` 的 `/usr/share`）、设备无 C 编译器（`runtime` 的 9 个）、上游 testdata 缺口（`go/internal/gccgoimporter`），外加 `runtime` 的 `TestTracebackSystem`（要 `-trimpath`，见 §5.2）。**除最后两项外，没有一条是 port 缺陷。**

**本节最重要的结论不是那 13 个包，而是覆盖率的教训**：这一节之前 sweep 是 226 PASS、看起来「已知类都解释完了」，而 §5.4 那个静默的 FIPS 缺陷**从来没被 sweep 抓到过**（`crypto/internal/fips140only` 全量轮一直 PASS）。**全绿 ≠ 没缺陷** —— 链接期/运行期成对的机制、以及只在特定 GODEBUG 下才走的路径，必须在设备上单独实测。

---

### 5.7 C 层 `test/` 全量实测（2026-09-22）：132 FAIL，其中 125 条是同一个链接器致命错误

B 层管「标准库能不能用」，C 层管「编译器/链接器对不对」——载体是
`go test cmd/internal/testdir -target=openharmony/arm64`，2734 个用例。

跑法（三条硬前提见 §3：用**仓库** `./bin/go`、`CC` 写 SDK clang 绝对路径、`$GOROOT/bin` 在 `PATH` 上）：

```bash
cd src
export OHOS_SDK=/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony
export PATH="$PWD/../bin:$PATH" CGO_ENABLED=1
export CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"
for i in 0 1 2 3; do
  hdc list targets                      # 模拟器卡死不自愈，每片跑前必查
  ../bin/go test cmd/internal/testdir -target=openharmony/arm64 \
      -shards=4 -shard=$i -timeout=180m
done
```

三个操作要点，都会咬人：

- **分片是唯一可靠的续跑手段**（模拟器会 474% CPU 空转卡死且不自愈）。卡了就重启，原样重跑该片。
- **`-shard` 是 0 基的。** `shardMatch`（`testdir_test.go:161`）是
  `int(h.Sum32()%uint32(*shards)) == *shard`，所以 `-shards=4` 必须跑 `-shard=0 1 2 3`。
  跑 `1 2 3 4` 会**静默漏掉 shard 0 的约 690 例**，而第 5 片只报一句
  `nothing to test on shard index 4`（`testdir_test.go:1954`）。**这个坑真踩过一轮。**
- `-timeout=180m` 是**宿主侧**的（跑的是宿主测试二进制），兜的是 hdc 挂死。

结果 —— 两次独立全量跑逐例一致：

| | 数 |
|---|---|
| RUN | **2739** |
| FAIL（子测试） | **132** |
| 与之对应的 `testdir_test.go:151: exit status 1` | **132** |

口径：FAIL 用 `grep -c '^    --- FAIL: Test/'`（**4 空格缩进 = 子测试**）。
别用 `^--- FAIL`：父测试 `--- FAIL: Test` 每片各 1 条，混进去会多算 4。分片是划分而非复制，
所以 132 条**互不重复**。

**132 条的构成（125 + 7，算术闭合）**：

| 类 | 条数 | 定性 |
|---|---|---|
| `link: cannot handle R_ARM64_TLS_IE (sym runtime.load_g) when linking internally` | **125** | **testdir 载体限制，非 port 缺陷**（§5.7.1） |
| 其余 7 条各自不同 | **7** | 逐条见 §5.7.3 |

#### 5.7.1 那 125 条：是「缺省 `-buildmode` 的链接」，不是 port 缺陷

testdir 的 `rundir` / `errorcheckandrundir` / `buildrundir` 三个 action **不走 cmd/go**：
它们自己 `go tool compile`，再用 `linkFile`（`testdir_test.go:242`）直接调 `go tool link`：

```go
cmd := []string{goTool, "tool", "link", "-s", "-w", "-buildid=test", "-o", outfile, "-importcfg=" + importcfg}
// 注意：没有 -buildmode
```

传递链是闭合的：

1. 无 `-buildmode` → `cmd/link/internal/ld/main.go:285` 对未设值取 `exe`；
2. `IsPIE()` 就是 `BuildMode == BuildModePIE`（`cmd/link/internal/ld/target.go:51`）→ **假**；
3. 于是 `cmd/link/internal/arm64/asm.go:970` 的 `if target.IsPIE() && target.IsElf()` 走 else，落到
   `asm.go:1010` 的 `log.Fatalf("cannot handle R_ARM64_TLS_IE (sym %s) when linking internally", …)`。

而 `R_ARM64_TLS_IE` 会被发出来，是因为 OHOS 的运行时是 **iscgo** 构建
（`runtime/tls_arm64.s` 的 `load_g` 走 IE/TLS 宏），stdlib 的 `.a` 里带这条重定位。
**非 PIE 的内部链接没有实现这条重定位，是这个移植的已知空档；但 cmd/go 从不请求它** ——
`platform.DefaultPIE` 对 openharmony 返回 true（`internal/platform/supported.go:242`，
`case "android", "ios", "openharmony": return true`），默认 `go build` 出来就是 PIE。
所以暴露面只限「testdir 这条手搓链接路径」。

判据三条：

1. 同一份代码走 cmd/go（默认 PIE）链得成；显式 `-buildmode=exe -ldflags=-linkmode=internal` 也链得成。
2. cmd/go 从不把 openharmony 的默认 buildmode 设成非 PIE（`DefaultPIE`）。
3. 上游 `linkFile` 对**任何**平台都不传 `-buildmode`，所以这条空档的暴露面天然限于 testdir。

**不修的理由**：修它要么给 OHOS 补一套非 PIE 的 TLS_IE 内部链接（工作量大，且没有真实用户路径），
要么改 testdir 按目标平台传 PIE（动上游测试框架，还会把「非 PIE 内部链接不可用」这个事实盖掉）。
记成已知空档，升级路径见 §5.7.3。

#### 5.7.2 本轮顺带修掉的 2 个真缺陷

**（1）三个 TLS objcheck 测试的检查从未运行过（port 自己写的测试）。**
`test/tls_le.s` / `tls_ie.s` / `tls_gd.s` 是移植时新增的，用来验证 `R_ARM64_TLS_{LE,IE,GD}`
真的被汇编器发出来了。它们的 asmcheck 模式之间用了**逗号**当分隔符，而 testdir 有一条 lint
（`testdir_test.go:1756`）明确拒绝 `",` 与 `` `, ``（`comma separator - use space instead`）。
于是这三个用例**每次都在解析阶段就报错**，
**三种 TLS 重定位的发射从来没被真正验证过**。改用空格后三条全 PASS —— 也就是说，
这是本次 C 层实测**新拿到的验证**，不是回归。

**（2）`-exec` 包装会凭空补一个换行。** 详见 §4.2 的「stdout 保真度」与
`misc/go_openharmony_exec/exitcode_test.go` 的 `sentinel glued to output` 用例。
C 层把它暴露成 `test/typeparam/issue50109.go`：`.out` 是 17 字节的无换行输出，回程变成 18 字节。

#### 5.7.3 剩下 7 条逐条

| 用例 | 报错 | 定性 |
|---|---|---|
| `fixedbugs/bug369.go` | `open : no such file or directory` | 包装 env 缺口：`STDLIB_IMPORTCFG` 未透传 → `os.ReadFile("")` |
| `linkobj.go` | `listing stdlib export files: open : no such file or directory` | 同一根因（§5.6 已记的 env 白名单） |
| `fixedbugs/issue10607.go` | `BUG: linkmode=external exit status 1` | 需 cgo 外部链接，而设备侧 `go build` 没有 cgo（§5.6 新增类 1 的同源） |
| `fixedbugs/issue46234.go` | `fork/exec ./a.exe: exec format error` | `buildrun` 在**宿主**上执行产物、未过包装 → 宿主跑不了 aarch64 |
| `fixedbugs/issue21317.go` | `failed to match "7:9: declared and not used: n"` | errorcheck 期望不匹配，**尚需单独看**（本轮未定性） |
| `nilptr.go` | `panic: dummy too far out` | 运行时信号测试（`run` action），设备上的崩溃行为与预期不符 |
| `nul1.go` | `string not terminated` | `hdc shell` 回程在 NUL 处截断（§4.2 已知限制）；用例本身就是要造 NUL 源码 |

**结论：C 层 132 条 FAIL 里没有一条指向新的 port 缺陷**（两条真缺陷见 §5.7.2，已修）。
`issue21317.go` 与 `nilptr.go` 两条尚未定性到底，是下一轮要先看的两条。

#### 5.7.4 覆盖率的教训（和 §5.6 同一条，但方向相反）

**95% 的 C 层 FAIL 是同一句 FATAL，而它挡在链接期。** `rundir` 系列的用例
（含全部 `codegen` / 泛型 `typeparam` 用例）**编译发生了、运行没有**。所以
「125 条不是 port 缺陷」**不等于**「这 125 个用例在 arm64 上的行为被验过了」——
它们验的是**编译器**（以及 asmcheck/objcheck 那部分「看生成的指令」的价值），
**没验运行期**。

这是个反向的覆盖率陷阱：§5.6 那次是「sweep 全绿 ≠ 没缺陷」，
这次是「**sweep 全红 ≠ 都验过了**」——同一个数字（FAIL 数）既可能藏缺陷，
也可能藏**未覆盖**。要把运行期补上，只能让 testdir 走 PIE 链接（或让 OHOS 支持非 PIE 内部链接）。

---

## 6. 耗时估算（为什么必须分批）

单设备串行。每个 std 包 = build + push + run：

- build：本机，快，但 260 个包累积可观
- push：std 测试二进制典型 2–10 MB，hdc 到模拟器约 1–3 s
- run：方差极大 —— `time` 几秒，`net`/`crypto/tls` 几十秒，`runtime` 4 分钟以上

粗估 **平均 15–25 s/包 → 1.5–3 小时**（B 层）。
C 层 ~1220 例设备执行，**2–5 小时**。

**全量一轮 = 半天到一天。** 这个数字决定了方案形态：必须有分层、有续跑、有单例超时，否则跑到一半挂掉就得从头来。

---

## 7. 分阶段落地

### 阶段 0 —— 未知数消解（先做，决定后面怎么写）

1. ~~**`-target` 是否真在设备上跑 `// run`？**~~ —— **已验，答案是「真跑」**（2026-09-21）。三组对照：

   | 条件 | 结果 |
   |---|---|
   | 真设备 | 9 个 `// run` 全 PASS，每例 2–3 s |
   | `OHOS_TARGET` 指向黑洞 | 同样用例全 FAIL，exit 125，`no exit code from target …（is the device connected?）` |
   | PATH 去掉包装目录 | **本格原结论是错的，2026-09-21 更正**：包装确实被禁用，宿主直接 exec 交叉二进制 → `fork/exec …: exec format error`。见下方纪律 |

   对照组里唯一 PASS 的是 `test/makechan.go`，它是 `// errorcheck`（只编译）—— **区分度正好落在「要不要执行」这条轴上**，说明不是碰巧通过。设备侧 `/data/local/tmp/go_openharmony_exec` 也确实被建出来。

   **推论：C 层按原设计做，不降级。** ~1220 例设备执行是真实可跑的，不是「只验交叉编译」。
   **更正一条纪律**：本节原先写「剥 PATH 无法禁用包装，因为 `$GOROOT/bin` 会兜底」—— **错**。`pathcache.LookPath` 是裸 `exec.LookPath`，只查 PATH。当时那轮对照之所以还调到了包装，是因为 `$GOROOT/bin` 本来就在 PATH 上，剥掉的不是它；而本轮漏设 PATH 时立刻退化成 `exec format error`，正好反证。想禁用包装，剥 PATH 是有效的；`OHOS_TARGET` 指黑洞更安全，还能顺带验证假通过防线。
2. ~~**包装能不能扛 `go test` 的并发？**~~ —— **已验，能**（2026-09-21）。默认 `-p` 跑 20 包，`flock` 把 hdc 访问串行化，**无报错、无死锁**。耗时分布呈现排队特征（多个包 elapsed 都聚在 ~21 s，是等锁不是自己跑），但结论是「默认 `-p` 可用，不必手工降到 1」。真正的吞吐数字要等阶段 2 带上 `runtime`/`net` 这类重包再测 —— 本轮全是小包，26 s 跑完 20 个，**不能外推**。
3. ~~**`crypto/x509` + `SSL_CERT_FILE` 能不能过？**~~ —— **结论：这条路走不通，不是因为证书，是因为 env 根本下不到设备。** 包装的 `run()` 不设 `cmd.Env`，宿主设 `SSL_CERT_FILE` 对设备进程零影响。**处方作废**，已回写第 5 节。要验 x509 只能改包装（加白名单式 env 透传，android 版就是这么做的）—— **这一条仍未修**。
4. **~~包装的 cwd / testdata 缺口要不要修？~~** —— **已修，选的是「先补包装再跑全量」那条路**（§9 的 (a)）。2026-09-21 完成并实测：

   | 改动 | 内容 |
   |---|---|
   | `pushSourceTree()` | `go test` 把包装的 cwd 设成包目录，这是「测试以为自己在哪」的唯一线索。包装现在把该目录镜像到 `<work>/cwd/<宿主绝对路径>`（保持宿主布局，无需 import-path 记账），再 `cd` 进去跑 |
   | 每进程工作目录 | 从 `<deviceRoot>/<bin名>` 改成 `<deviceRoot>/<bin名>-<pid>`，避免同包不同模块的两次构建抢同一路径 |
   | `hdc()` 不再只看退出码 | **hdc 失败时打 `[Fail]` 到 stdout 却仍 exit 0**（实测：`file send` 到不存在的目录 → `[Fail]Error opening file…`，`exit=0`）。原先的 `fail("push", err)` 因此永不触发。现在捕获 stdout 并识别 `[Fail]` |
   | 回归防线 | `runmain_test.go` 用假 hdc 驱动整个 `runMain`；两条断言（推了源文件树 / 从镜像 `cd` 进去）各自做过变异验证，改坏哪条哪条红 |

   **实测兑现**（`PATH` 带 `$GOROOT/bin`、`CC` 用 SDK 绝对路径）：
   - `io/fs` → ok 0.875 s，`text/template` → ok 1.330 s（修前：`io/fs` 的 `TestGlob` 拿根目录当测试目录、`text/template` 找不到 `testdata/`）
   - `os` → 20.7 s 跑通（修前连 cwd 都过不去）。剩余 FAIL 全部落在第 5 节的「环回 TCP listen」与「设备权限模型」两类，**没有一类是 harness 缺口**

   **顺带证实**：`$GOROOT/bin` 不在 `PATH` 上时，包装根本不被调用，宿主直接 `exec format error` —— 见 0.1 的更正。

### 阶段 1 —— 冒烟（分钟级，每次改动都跑）

`probe` + 少量关键包（`strings runtime os net/url internal/platform`）+ `misc:execwrapper` 回归。
对应 §4.2 的「升级后先跑 probe」。

### 阶段 2 —— 核心（~30 分钟）

`runtests.sh`（无参数即本层）。`runtime` + OHOS delta 触及的全部包（§4.1 清单逐条对应：`internal/platform`、`cmd/go/internal/work`、`net`、`time`、`mime`、`os`、`testing`）+ 平台敏感包（`os/exec`、`syscall`、`net/http`）。

**2026-09-21 已跑过一轮**：6 PASS / 7 FAIL / 0 TIMEOUT / 0 WRAPPER，**无 port 缺陷**，逐条 triage 见 **§5.1**。

> 其中 `internal/platform` 与 `cmd/go/internal/work` 是 **A 层**（宿主测试，验的是编译器与平台表），交叉编译再送上设备没有意义 —— §3 要求别把三层混在一起，它们归 `./all.bash`，所以**不在脚本的 `CORE` 里**。

### 阶段 3 —— 全量（小时级，发版/升级后跑一次）

`runtests.sh --all`（B 层 262 包）+ C 层全量（`-shards=4`，见 §5.7）。**2026-09-21/22 已各跑一轮**：

| 轮 | 层 | 结果 | triage |
|---|---|---|---|
| 2026-09-21 | B 层（v1 基线） | 226 PASS / 36 FAIL | §5.3（36 条全落已知类；真缺陷 FIPS **不在这 36 条里**，见 §5.4） |
| 2026-09-21 | B 层（v3，镜像 GOROOT 后） | **239 PASS / 23 FAIL** | §5.6 |
| 2026-09-22 | C 层（`test/`） | **2739 RUN / 132 FAIL** | §5.7（125 条是 testdir 载体限制，7 条逐条有据） |

两轮合计**没有一条未定性的 port 缺陷**。上一代（go1.26.5）此处的记录是「224 PASS / 38 FAIL / 1 条真缺陷（FIPS）」——
注意那条 FIPS 缺陷是**靠 §5.4 那种定向验证**才现形的，全量轮当时是绿的。

脚本已兑现的特性：

- **per-case 超时**，卡住的用例不拖垮整轮。两层：设备侧 `go test -timeout` + **主机侧看门狗**（`-t`，默认 300 s）。后者不可省 —— `go test` 自己的 `-timeout` 是设备侧测试二进制执行的，hdc 一卡，主机侧没有任何东西会杀掉这次调用
- **可续跑**：结果落盘（每包一行 `pkg status seconds skips`），重跑跳过已 PASS（`--force` 重跑）
- **SKIP 计数**：`-v` 时统计 `--- SKIP` 数落进 `results.tsv`。SKIP 突然变多 = 测试在悄悄失效
- **退出码语义**：`0` 全绿；非零 = 有 FAIL/TIMEOUT/WRAPPER。**不允许「设备没回传状态」被算成 PASS** —— 这正是 `480afb8fc47` 修的那个 bug
- **失败分类**：PASS / FAIL / TIMEOUT / WRAPPER 四态。**WRAPPER 单列很值**：它代表「包装或设备连接本身出错」（如目标不可达），和「测试跑了但挂了」是完全不同的排查方向，混进 FAIL 会把信号淹没。注意 `go test` 会把包装的 125 压成 1，所以**只能按日志里的 `go_openharmony_exec:` 前缀认**，不能按退出码

---

## 8. 验收标准

「全量可用」的判定不能是「脚本退出码 0」，那太容易被假通过污染（该 bug 已经发生过一次）。定为：

1. B 层 260 包全部有结论（PASS / FAIL / SKIP+原因），**无「未执行」**
2. FAIL 集合 ⊆ 第 5 节已知清单，或每条 FAIL 都有新记录的原因
3. C 层可执行部分全绿，或 FAIL 同上有据
4. 阶段 1 的 probe 显示设备事实未退化（`vma_bits_*`、split identity、CA 位置）
5. 汇总里 **SKIP 数必须显式打印**；SKIP 突然变多等于测试在悄悄失效

---

## 9. 风险

| 风险 | 说明 |
|---|---|
| ~~harness 缺陷淹没真信号~~（**已闭环**） | 原风险：包装缺「建工作目录 + 推源文件树 + 下发 env」，会让 cwd/testdata/env 三类测试成片 FAIL。三项**均已补掉并实测兑现**：cwd/testdata 见 §7 阶段 0.4，GOROOT 整树镜像 + 设备原生 `go` + env 白名单见 **§5.6（36 FAIL → 23 FAIL）**。**残留**：`SSL_CERT_FILE` 已能下发，但设备 `/etc/ssl/certs` 是 EACCES，`crypto/x509` 仍不可测（真·设备权限，非 harness 缺口）。「环回 TCP listen 被禁」「设备权限模型」**必须与 port 缺陷分开标注** |
| **旧工具链污染测量**（已中过一次） | `~/go1.27.1-ohos` 早于 `2417e0834a9`，本轮第一遍测量全部作废重跑。**跑之前先核对工具链出处**（`git show HEAD:src/internal/platform/supported.go | grep -A2 'func DefaultPIE'` 应含 `openharmony`），别信目录名里的版本号。 |
| **下游拿到过期工具链** | `~/go1.27.1-ohos` 就是 v2rayHM 的 `OHOS_GO_ROOT`，它缺 PIE 修复与 FIPS 修复（构建于 09-19 16:52，两个提交分别在 21:24 / 更晚）。**2026-09-22 做了 A/B 实测来界定波及面**（同一个 `cgomin`/`cshared` 夹具，只换工具链，都在模拟器上跑）：<br>• **c-shared `.so`（v2rayHM 实际产出的形态）：过期树产物 `Add(40,2)=42` / exit 0 —— 不受影响。** c-shared 走外部链接（clang 按 `--target` 自选 loader）且 `Flag_shared` → TLS 走 GD；`.so` 本身也没有 `PT_INTERP`（实测两者都是 0 段）。<br>• **cgo 可执行文件：过期树产物 `Signal 11` / exit 139，仓库树 `main reached` / exit 0 —— 确实坏。**<br>所以**别笼统说「下游会 Signal 11」** —— 受影响的是**可执行文件**（`go test` 二进制也是这一类），而 v2rayHM 编的是 `.so`。仍建议刷新那棵树（配方见 `docs/go-upgrade-guide.md` §6.7），但**不是**「一放出去就有人踩」的阻断项。 |
| 假通过 | 已发生过一次（`480afb8fc47`）。任何「结果解析」路径都要有回归测试，新脚本复用 `newExitFilter` 而不是自己拼 |
| **模拟器会卡死，且只此一台**（已中过一次） | 2026-09-21 全量轮启动后 **~1 分钟即全包 `WRAPPER`**：`hdc` 报 `[Fail][E001005] Device not found or connected`（`hdc list targets` 空、`tconn` 拒连），而 `Emulator -start Pura 90 API24` 进程仍在、`Rs` 状态吃 **474% CPU** 空转 —— **不会自愈，只能重启**。真机 `4VM0125513000074` 因 HMMAC 拒 exec 顶不上来，所以这一台挂了全量就停摆。<br>**缓解**：`runtests.sh` 的续跑正是为此 —— 重启设备后原样再跑一次即可，已 PASS 的自动跳过，不用从头来。**注意 results.tsv 里会出现同一包的多行**（脚本是追加写），分析时按「后写的覆盖先写的」读。建议先把 probe 存基线以便比对漂移。 |
| **宿主链接器换了，链接器的假设要重验** | §5.4 的教训值得推广：port 为 openharmony 选了 **lld**（`lib.go:1727`），于是 `cmd/link` 里任何**「最终 ELF 长什么样」的假设**都可能不成立。FIPS 只是撞上的那一个 —— `elffips` 假定 RELATIVE 的 addend 已预存进 section 数据，lld 不那样写。**同类可疑点还有 `decodesym.go` 的 `decodetypeGcprogShlibByReloc` 等直接读外部链接产物的地方**（该点 **2026-09-21 已复验并修掉两处缺陷，见 §5.5**），下次升级上游或换 SDK 版本时仍要优先复验。判据：**凡是「链接期算一遍、运行期再核对一遍」的机制（完整性校验、校验和、签名）都要在设备上实测**，链接期不报错不代表对 |
| 上游测试假设宿主 | std 测试大量用 `testenv` 自跳过，SKIP 率高不一定是坏事，但必须能看见 |
| ~~C 层未知数~~ | **已消解**，见阶段 0.1 —— `-target` 确实在设备上执行 `// run`，C 层不降级 |
