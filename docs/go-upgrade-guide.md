# OHOS Go 工具链升级指南（go1.24.5 → go1.26.5）

本文档总结把上游 Go 新版本合并进 OpenHarmony 分支（`ohos-1.24-base`）的完整流程、关键要点、踩过的坑与验证方法。后续升级 Go 版本（如 1.27、1.28）时可直接复用本流程。

---

## 1. 背景与仓库结构

| 项 | 值 |
|---|---|
| 开发分支 | `ohos-1.24-base`（OHOS go1.24.5 树，基线 `67b6cc417b`） |
| 上游仓库 | `upstream` = github.com/golang/go |
| OHOS 参考 | `sig` = gitcode.com/openharmony-sig/ohos_golang_go |
| 发布仓库 | `origin` = github.com/star4277/ohos-go |
| 主分支 | `main`（vintage OHOS 树，历史与开发分支无关，需靠 merge 提交桥接） |

### 关键模型

- OHOS 端口：`openharmony/amd64` + `openharmony/arm64`
- 逻辑 GOOS 为 `openharmony`，但 `runtime.GOOS == "linux"`、`runtime.IsOpenharmony == true`
- OHOS 核心改动：musl emulated-TLS（TLS 描述符 / `__tls_get_addr` / TSD via pthread_key）
- clang 是首选 CC，多条路径需要 external linking

---

## 2. 合并流程总览

```
1. 提取 OHOS delta（相对 go1.24.5 基线）
2. 拉取上游目标版本 tag
3. git merge tag（产生冲突）
4. 分类冲突：
   a. 纯上游文件  → 直接取 theirs（上游版本）
   b. OHOS 改动文件 → 取 theirs + 重新套用 OHOS hunks
5. 解决所有冲突并 git add
6. 清理冲突标记（git grep '<<<<<<< HEAD|>>>>>>>'）
7. make.bat 自举构建（用官方 go）
8. 修复编译错误
9. gofmt + go vet + 跑测试
10. 验证 OHOS 行为保留
11. 提交 + 推送 + 建 PR（需先 merge main 桥接历史）
```

---

## 3. 冲突处理方法论（最重要）

### 3.1 先提取 OHOS delta

升级前先从当前 OHOS 基线提取权威 delta，作为重新套用的依据：

```bash
git diff <上游旧版本基线> <当前OHOS树HEAD> > ohos-delta-vs-旧版本.patch
```

delta 保存到临时目录（如 `C:\Users\Administrator\AppData\Local\Temp\opencode\ohos-merge\`），每个混合冲突文件单独存一份 `ohos-<path with /→__>.patch`。

### 3.2 冲突分类

| 类别 | 处理方式 |
|---|---|
| 纯上游文件（OHOS 未改） | `git checkout --theirs <file>` 直接取上游 |
| OHOS 修改过的文件 | 取 theirs（上游版本），再手动重新套用 OHOS 专属 hunk |
| OHOS 新增文件（`rt0_openharmony_*.s` 等） | 保留 |

### 3.3 判定标准

- 用 `git diff <上游tag> -- <file>` 检查最终结果：残留的 diff 应**恰好等于** OHOS delta 中该文件的部分
- 某些 OHOS 改动在上游新版本已原生实现 → 应取 theirs（零 diff 才算正确）

### 3.4 曾被 subsumed 的 OHOS 改动（上游已原生实现，直接取 theirs）

- `doc/godebug.md` + `godebugs/table.go`（httpcookiemaxnum）
- `crypto/x509/parser_test.go`、`verify_test.go`、`verify.go`（约束匹配移到泛型 constraints.go，取代 OHOS reversedDomainsCache 优化）
- `encoding/pem/pem.go`（OHOS 加的 `getLine` 上游已有）
- `os/exec/lp_plan9.go`、`lp_windows.go`（lookPath 重构上游已含 validateLookPath）
- `runtime/runtime2.go`（allpSnapshot 上游已原生）
- `net/url/url.go`、`url_test.go`
- `runtime/crash_cgo_test.go` 的大部（1.26.5 重构为 race.Enabled 跳过）

### 3.5 文件存在性校验（`git diff` **看不见**的一种缺失）

`git diff <上游tag>` 只比**两边都跟踪的文件的内容**。它不会告诉你「上游跟踪了、本地根本没这个文件」——
那种文件在 diff 里**什么都不显示**，除非你反过来按上游路径逐个 `[ -e ]`。

而 .gitignore 正好能造出这种缺失：`.gitignore` 第 2、3 行是

```
*.[56789ao]
*.a[56789o]
```

它匹配**上游真实签入的二进制 testdata**。这些文件在上游是 `git add -f` 进去的，一旦本地树里丢了，
`git add -A` / `git add .` **永远加不回来**，`git status` 也不报缺 —— 静默。

**实测代价**（2026-09-22，A 层全量跑出来的）：fork 导入时丢了 **10 个**上游跟踪的二进制 testdata，

```
src/cmd/objdump/testdata/go116.o                      → TestGoObjOtherVersion 挂
src/go/internal/gccgoimporter/testdata/libimportsar.a → TestGoxImporter 挂
src/go/internal/gcimporter/testdata/versions/test_go1.{7,8,11}_*.a  （8 个）
```

最后 8 个更阴：`go/internal/gcimporter` **不报 FAIL，只报 `ok`** —— 它靠文件缺席来跳过版本用例。
补齐后同一包从 6.3s 变 27.7s，**才真的在跑**。「全绿」在这里同样会藏东西。

**合并后必做这一步**：

```bash
gh api 'repos/golang/go/git/trees/<上游tag>?recursive=1' \
  --jq '.tree[] | select(.type=="blob") | .path' | LC_ALL=C sort > /tmp/up.txt
git -c core.quotePath=false ls-files | LC_ALL=C sort > /tmp/local.txt   # quotePath 会把非 ASCII 转义成八进制
comm -23 /tmp/up.txt /tmp/local.txt        # 输出必须为空
```

`comm` 有输出 = 上游有而本地缺，**必须是 0 行**（本地多出来的行是 OHOS 新增文件，属正常 delta）。
补文件时按上游 blob sha 校验（`git hash-object`），别信「看起来一样」。

---

## 4. OHOS 核心特性清单（升级时必须保留）

### 4.1 升级时必须保留的特性

升级后务必逐项核对以下能力仍在：

| 特性 | 关键位置 |
|---|---|
| musl TLS 检测 | `runtime/os_linux.go` 的 `_cgo_is_musl` / `libmusl` / `libpreinit` |
| musl 环境变量 | `runtime1.go` 的 `procEnviron` / `muslEnviron` / `goenvs_musl` / `readNullTerminatedStringsFromFile` |
| TLS_GD reloc | `objabi/reloctype.go` 的 `R_AMD64_TLS_GD`、`R_ARM64_TLS_GD`（编号靠后追加） |
| amd64 TLS 描述符 | `cmd/internal/obj/x86/asm6.go`（`ctxt.Tls=="GD" || (isOpenharmony && Flag_shared)`） |
| arm64 TLS_GD | `cmd/internal/obj/arm64/asm7.go`（case 编号需避开上游已占用的号） |
| openharmony 平台登记 | `internal/platform/zosarch.go`、`supported.go` |
| **amd64 强制外链** | `internal/platform/supported.go` 的 `MustLinkExternal`（`case "openharmony": if goarch != "arm64"`）**及 `cmd/dist/build.go` 的引导期副本**。两处必须同时保留 —— `cmd/dist/build_test.go` 的 `TestMustLinkExternal` 逐格比对两者，漏一处就红。丢掉它，amd64 的**每次**默认构建都死在 `cannot handle R_AMD64_TLS_GD ... when linking internally`（见 §4.3） |
| goos 映射 | `cmd/go/internal/cfg/cfg.go`、`cmd/go/go_test.go` 的 `goos`、cmd/dist |
| c-shared 入口 | `runtime/rt0_openharmony_amd64.s`、`rt0_openharmony_arm64.s` |
| 共享库 GC 数据 | `cmd/link/internal/ld/decodesym.go` 的 `decodetypeGcprogShlibByReloc` |
| 测试标签 | `testing/benchmark.go` 的 goos 打印、`crash_cgo_test.go` 的 openharmony case |
| 链接器跳过 | `cmd/link/link_test.go`（race detector skip `|| runtime.IsOpenharmony`） |
| 默认 PIE | `internal/platform/supported.go` 的 `DefaultPIE`（`case "android", "ios", "openharmony"`）——去掉它，cgo 可执行文件就会退回 `Signal 11` |
| ELF 解释器 | `cmd/link/internal/ld/elf.go` 的 `case objabi.Hlinux`（openharmony → 直接用 `LinuxdynldMusl`，**不做宿主探测**） |
| **`go.env` 的 `GOTOOLCHAIN=local`** | 仓库根的 `go.env`。**这个文件就是 GOROOT 根的文件**（`bin/go` 按自身位置推 GOROOT），所以它逐字就是发布出去的值。上游写的是 `GOTOOLCHAIN=auto` —— 留着它，用户模块里只要有一个依赖声明了更高版本，`go` 就会**去下载官方工具链**（实测会打 `go: downloading go1.28.0`），而官方工具链编不出 `GOOS=openharmony`。合并时这行会被上游盖掉，而且**症状只出现在下游、不在本仓库**，所以必须靠对账 |
| CI | `.github/workflows/ohos.yml` —— 上游 `golang/go` 没有 workflows，合并不会冲突，**但它守的是这张表本身**（amd64 外链、DefaultPIE、平台登记都被它断言）。丢了它，这张表就退回「只有散文记录」 |

### 4.2 能力实测结论（2026-09，go1.27.1）

设备：模拟器 `127.0.0.1:5555`（`HongMeng Kernel`/Linux 5.10.210，SDK clang 15.0.4）。
夹具与执行包装在 `misc/openharmony/`：`ohosrun` 负责推送执行，其余每个目录一个能力。
判定原则：**链接成功 ≠ 能力可用**，下表每行都附设备上的实测证据。

| 能力 | 结论 | 证据 / 备注 |
|---|---|---|
| `c-archive` | ✅ 可用 | `cdriver link` 静态链 `.a` → `Add(40,2)=42` |
| `c-shared` | ✅ 可用 | `dlopen(RTLD_NOW)`+`dlsym` 与链接期 `-ladd` 两条路都 → 42 |
| `plugin` | ✅ 可用 | `plug.so` + PIE 宿主 `plugin.Open`→`Lookup` → 42 |
| `shared` | ✅ 可用 | `libstd.so`（74 MB）+ `LD_LIBRARY_PATH` → `-linkshared` 宿主打印 42 |
| **cgo 可执行文件** | ✅ 默认即可（无需再加 `-buildmode=pie`） | 2026-09 起 `platform.DefaultPIE` 对 openharmony 返回 true，默认 buildmode 本身就产出可运行的 PIE。改之前默认 buildmode 下任何 `import "C"` 的程序在 `init()` 之前 `Signal 11`（exit 139）。最小复现：`misc/openharmony/cgomin` |
| `-asan` | ✅ 可用（需 PIE，默认 buildmode 现已满足） | 设备上打印 `ERROR: AddressSanitizer: heap-buffer-overflow` + `0 bytes to the right of 4-byte region`；§7 的命令仍显式写 `-buildmode=pie`，无害 |
| `-race` | ❌ 不开门 —— **「不移植」是决策，不是欠债** | **先定性**：`-race` 是开发期诊断工具，交付产物永远不带它（慢 5~20 倍、内存翻 5~10 倍）。缺它影响的是「能不能在设备上跑竞态检测」，不是「能不能在设备上跑 Go」—— 下游 v2rayHM / xray / sing-box 的发行产物没有一个用 `-race` 编。**为什么不开：四层闸。** ① 我们：`platform.RaceDetectorSupported` 无 openharmony case → 构建期一句干净报错（上游 arm64 一律返回 true，因为它编译期不知道 VMA 大小 —— 我们拦住更合理，**保持不动**）。② `runtime/race/race.go` 的 build constraint 只列 `linux`。③ race runtime 是**预编译 blob**（`runtime/race/*.syso`），按文件名蕴含的 `linux && arm64` 选中，仓里**没有 C++ 源码**，是 LLVM compiler-rt 的 `SANITIZER_GO` 产物、靠 `.patch` 跟 LLVM 走。④ `runtime/malloc.go` 把 race 堆硬编在 `[0x00c000000000, 0x00e000000000)`＝824 GiB，39 位（512 GiB）装不下（riscv64 有 39 位分支，arm64 没有）。**上游立场**（firecracker#3514 关成 not-a-bug、golang/go#29948 同）：*"if Go does not support the smaller address space, then your own change to the kernel configuration is a requirement"* —— 平台自己调 VMA，Go 不适配。**替代路径**：竞态在宿主上验（`GOOS=linux GOARCH=amd64 go test -race`，不需要 OHOS SDK，见 `docs/ohos-release-roadmap.md` §④）；原生胶水层另走 C++ 侧 `-fsanitize=thread`（**非** `SANITIZER_GO` 的 aarch64 TSan 收 39 位，与 Go 那条线不同）。**何时重估**：OHOS 改 VA / 页大小配置时。`ARM64_VA_BITS_39` 在 Kconfig 里 `depends on ARM64_4K_PAGES` —— 39 位绑死 4KB 页，16KB 页下必然 ≥42 位（3 级）或 47 位（4 级），而 47 在 Go 的允许集里。**这条是推断，未验** —— 拿到 16KB 页设备时顺手打 `probe` 的 `vma_bits_*` 和 `go test -race` |
| `-msan` | ❌ 不可行 | SDK 里 `libclang_rt.msan*` 为零，且 MSan 要求插桩过的 libc，OHOS musl 不是 → 每个 libc 调用都会假阳性。`MSanSupported` 的 `default: return false` 保持不动 |
| SVE | ✅ 汇编器可用（需 `GOEXPERIMENT=simd`） | `ZADD Z7.D, Z23.D, Z13.D` 为 `openharmony/arm64` 编出 `04e702ed`，与上游 `arm64sveenc.s` 期望的 `ed02e704` 逐字节相符。硬件执行未验：shell uid 读不了 `/proc/cpuinfo` |
| x509 系统根 | ✅ **应用域直接可用，不需要任何 env** | 本条原先记的是「❌ 外部进程取不到，正解是 `SSL_CERT_FILE`」—— **那是把 `sh` 域的载体限制当成了平台限制**，与下一行的环回是同一类错误（`sh` 域观测到的 `open /etc/ssl/certs: permission denied` 不能外推到应用域）。2026-09-22 用 `loopbackhap` 夹具在应用域（uid `20020077`）按 Go 的真实读取路径逐环验：`ReadDir("/etc/ssl/certs")` OK（1 条目 `cacert.pem`）→ 该条目 `lstat` **非软链**（否则会被 `readUniqueDirectoryEntries` 丢掉，成空池静默失败）→ `ReadFile` OK（191450 B，真 PEM）→ 同一份字节在宿主 `AppendCertsFromPEM` **125/125**。故 `root.go:199` 返回非空池。**不加 `root_openharmony.go` 这个结论是对的，但理由与当初写的相反：不是「取不到所以用 env」，是「上游 `certDirectories[0] = /etc/ssl/certs` 本来就命中 OHOS 的 bundle 目录」。** 唯一真缺口：`/data/certificates/user_cacerts`（OHOS 用户 CA）应用域 EACCES → **用户自装 CA 取不到**，照抄 Android 那两行无效（问题是权限不是路径），要做得走 OHOS cert framework + cgo，是**真特性不是补丁** |
| 真机执行 | ⚠️ 受限 | 商用版真机 `4VM0125513000074` 的 shell 域不能 exec `/data/local/tmp` 下的未签名二进制（`Permission denied`）。本轮所有设备侧结论出自模拟器 |
| **应用域环回 bind/listen/accept** | ✅ 可用 | **由 `sh` 域测不出来**。夹具 `misc/openharmony/loopbackhap`（无窗口 UIAbility，`@ohos.net.socket`）以 uid `20020077`（`u:r:app:s0`）跑：A `bind 127.0.0.1:0`、B `bind ::1:0`、C `bind 0.0.0.0:0`、D `listen 127.0.0.1:39321`、E 对自己 `connect` 并**收到入站连接**，五个全 OK。旁证：`com.9bt.transmissionbtm`（uid `20020076`）在同一个模拟器上 bind 了 `0.0.0.0:51413`。故 B 层 17 包环回类 FAIL 是 **`sh` 权限域限制，非 port 缺陷**（详见 `docs/ohos-full-test-plan.md` §5.1 / §5.3 的环回类） |
| `-exec` 自动执行 | ✅ 可用 | `misc/go_openharmony_exec` 经 `go_<GOOS>_<GOARCH>_exec` 约定被 `go test` 自动发现：`GOOS=openharmony GOARCH=arm64 go test strings` → `ok strings 3.661s`。设备侧回程证据：`GOOS=linux GOARCH=arm64 IsOpenharmony=true`（**split identity 在运行中的设备进程里被观察到**），且故意失败的用例正确回传 `FAIL` + 非零退出码 |

**「设备上 bind 不了 127.0.0.1」这件事，要用应用域的夹具来判，不能靠 `sh` 下的测试**

B 层那 17 个包的环回类 FAIL 一度看起来像 port 缺陷（症状是 nettest 掩盖过的
`tcp is not supported on linux/arm64`，看不出真 errno）。判据换域才成立：同一个模拟器，
`sh` 域（`uid=2000`/`u:r:sh:s0`）起不了监听，应用域（`uid=20020077`/`u:r:app:s0`）五个探针全过。
**所以这类 FAIL 的成因是「跑测试的域」，不是移植代码。** 复现步骤、五个探针的含义、
以及判读规则（A/D 失败才算真缺陷）都在 `misc/openharmony/loopbackhap/README.md`。

**这条教训已经应验了两次**，别当成一次性的：同一个夹具后来又定案了 x509 ——
`sh` 域报 `open /etc/ssl/certs: permission denied`，表格据此记了「x509 取不到系统根」，
而应用域**读得到**（见上一行的 x509 条）。**凡是只在 `sh` 域观察到的路径/权限失败，
都不能直接归给平台** —— 该夹具的 `CertProbe` 与环回探针在同一次 `aa start` 里跑完。

两个操作要点：

- **模拟器不验 HAP 签名**：`hdc install -r entry-default-unsigned.hap` 直接 `install bundle successfully`，
  所以这条夹具**不需要 DevEco 签名、不需要驱动 GUI**。这是**模拟器的性质**；
  真机只信华为 CA，换真机必须重签（`~/.ohos/config/` 下那套 `*.p12/*.cer/*.p7b`）。
- 新夹具的 `build-profile.json5` / `entry/build-profile.json5` / `oh-package.json5` / `hvigorfile.ts`
  **从 DevEco 自带脚手架原样拷贝**（`plugins/codegenie-plugin/previewProject{Template}/`），
  理由和那个 `@ohos/hamock@1.0.1-rc2` 的坑见 README。

**为什么默认 PIE 还牵扯到解释器（两处必须同时存在）**

只加 `DefaultPIE` 会把**纯 Go 可执行文件**一起变成 PIE，而内部链接的 PIE 要写 `PT_INTERP`：`elf.go` 原来的逻辑是 `interpreter = Linuxdynld`（glibc 路径），再 `os.Stat` 它——**那是宿主文件系统上的探测**，macOS 上两个 loader 都不存在，于是沿用了 glibc 路径 `/lib/ld-linux-aarch64.so.1`，而设备上只有 `/lib/ld-musl-aarch64.so.1` → `execve` 报 ENOENT（`/bin/sh: ...: No such file or directory`）。cgo 的可执行文件不受影响，因为它们走外部链接、由 clang 按 `--target` 自己选 musl loader。openharmony 是纯 musl 目标，没有可探测的余地，所以直接取 `LinuxdynldMusl`。

两个必须记住的操作细节：

- **c-archive 在 macOS 上会静默产出 96 字节空库**：`/usr/bin/ar`（cctools）拒收 ELF 成员（`Inappropriate file type or format`），一个都不加却返回 0。必须 `AR=$SDK/native/llvm/bin/llvm-ar`。只有 c-archive 走 `ar`（`cmd/link/internal/ld/lib.go` 的 `archive()`）。
- **`-asan` 夹具的越界写必须 `volatile`**：Go 默认 `CGO_CFLAGS` 是 `-O2 -g`，`p[4] = 1; v = p[4]` 会被折叠成常量 1、写整个被删除（`malloc`/`free` 还在），于是根本没有越界可供检出。用 `volatile char *q = p + 4; *q = 1;`。

**装 `-exec` 包装**（普通 `make.bash` 不装它 —— `cmdbootstrap` 里 `goos` 此时等于宿主的 `gohostos`，`wrapperPathFor` 返回空；只有 `GOOS=openharmony ./make.bash` 走交叉自举分支才会自动装）：

```bash
cd misc && $GOROOT/bin/go build -o $GOROOT/bin/go_openharmony_arm64_exec ./go_openharmony_exec
cp $GOROOT/bin/go_openharmony_arm64_exec $GOROOT/bin/go_openharmony_amd64_exec
```

`$GOROOT/bin` 必须在 `PATH` 上，否则 `go` 的 `pathcache.LookPath("go_openharmony_arm64_exec")`（`cmd/go/internal/work/build.go:902`）找不到它。设备由 `OHOS_HDC` / `OHOS_TARGET` 选（默认 `hdc` 与模拟器 `127.0.0.1:5555`）。

**这一段为何要手装：** `cmd/dist/build.go` 的 `wrapperPathFor` 里 `openharmony` 那条分支与上游 android/ios 逐字同形，只在 `oldgoos != gohostos` 时命中 —— 即只有交叉自举（`GOOS=openharmony GOARCH=arm64 ./make.bash`）才自动装，普通自举下返回空。该分支**未在完整交叉自举中执行过**，但其载荷已按 dist 的原命令单验（`GOOS=darwin GOARCH=arm64 go build -o <tmp> misc/go_openharmony_exec/main.go`，rc=0，产物在宿主上行为正确），残差风险只有那 3 行 `goos` 管道，与 android 一致。

**回归测试已入 dist：** `src/cmd/dist/test.go` 注册了 `misc:execwrapper`（`./all.bash` 会跑），跑的就是本包装的退出码解析。它存在的原因是一次真实故障：`exitFilter.code` 的零值 0 与 `code < 0` 的判断让"设备没回传状态"分支成为死代码，于是设备根本没执行任何东西也会返回成功。改 `main.go` 的退出码路径时这条测试会挡下来。

**stdout 保真度（2026-09-22，C 层实测暴露后修掉的一处真缺陷）**

`exitFilter.line` 原来对**每一行**都写 `append(TrimRight(l,"\r"), '\n')`。对**行尾无换行**的程序
输出，那一行是被 `; echo $?` 的哨兵粘上来的（`boom__EXIT__3`），程序本身**没有**输出那个 `\n` ——
于是包装凭空多写一个字节。`cmd/internal/testdir` 的 `checkExpectedOutput`
（`testdir_test.go:1205`）是**逐字节**比较、只做 `\r\n`→`\n` 规整，所以 `test/typeparam/issue50109.go`
就是这么挂的：`.out` 是 17 字节的 `MySuperStruct`（无换行），回程变成 18 字节。
现在哨兵**粘行**的分支不再补换行（`exitFilter.line` 的 `glued`），`exitcode_test.go`
的 `sentinel glued to output` 那条断言同步从 `"boom\n"` 改成 `"boom"`。

**已知限制（不修，在 hdc 层之下）：hdc shell 传不了 NUL。** 实测：程序 `write("A\x00BC")`
（4 字节）经包装只回 `A`（1 字节）—— NUL 处截断，其后的字节一起丢。`test/nul1.go`
（`// errorcheckoutput`，就是要造 NUL 源码）因此必挂。要修得改成「设备侧 stdout 重定向到文件
再 `hdc file recv`」，动的是 `run()` 的核心路径，beta 阶段记成已知限制而不是冒险改。

### 4.3 amd64 必须外部链接（2026-09-22 修，对应 §4.1 表里的同一行）

`openharmony/amd64` 的**每次**构建都强制外部链接 —— `internal/platform/supported.go` 的
`MustLinkExternal` 与 `cmd/dist/build.go` 的引导期副本各一行 `if goarch != "arm64" { return true }`。

**为什么**：`cmd/internal/obj/x86/asm6.go` 的发射条件是
`ctxt.Tls == "GD" || (isOpenharmony && ctxt.Flag_shared)`，而 openharmony 默认 PIE
⇒ cmd/compile 拿到 `-shared`（实测：一轮默认构建里 465 次 compile 调用全带它）
⇒ **每次默认构建都为 g 寄存器重载发出 `R_AMD64_TLS_GD`**。内部链接器没有这条重定位的实现
（`ld/data.go:353` 直接 `log.Fatalf`），只有外部路径有（`amd64/asm.go` 的 `elfreloc1`，
发 `R_X86_64_GOTPC32_TLSDESC` + `R_X86_64_TLSDESC_CALL`）。

**代价**：amd64 的**纯 Go** 构建也要求 `CGO_ENABLED=1` + SDK clang，否则报
`openharmony/amd64 requires external (cgo) linking, but cgo is not enabled`。
与上游 `android/amd64` 的取舍逐字同形。**amd64 的 CC 要换 target**：

```bash
export CC="$OHOS_SDK/native/llvm/bin/clang --target=x86_64-linux-ohos \
  --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"
```

**arm64 不受影响**：`asm7.go` 只在汇编显式 `MOVW $tlsvar`（case 101）时发 `R_ARM64_TLS_GD`，
编译器产生的代码不走那条，默认构建里根本没有这条重定位，照旧内部链接。

**为什么不在编译器侧收窄**：PIE 与 c-shared 拿到的都是 `-shared`，编译器分不出来。

**实测**（2026-09-22）：改前 amd64 默认构建**必然** Fatalf；改后能编出产物 ——
`ELF64 / DYN(PIE) / X86-64`、`PT_INTERP = /lib/ld-musl-x86_64.so.1`（架构正确的 musl 解释器）、
`PT_TLS` 在位、动态重定位 **15922 `R_X86_64_RELATIVE` + 0 条 TLSDESC**、
反汇编里 GD 惯用法（`call *(%rax)`）**0 次**、`%fs:` 段访问 **778 次**
⇒ **lld 把 TLSDESC 松弛成了 LE**，与 arm64 PIE 已在模拟器上跑通的模型一致；
而 arm64 的 **c-shared 库保留 1 条 `R_AARCH64_TLSDESC`**（被 dlopen 的库必须走描述符）。
GD 出现在需要它的地方、LE 出现在安全的地方，**由链接器决定** —— 这正是外部链接不可替代的理由。

---

## 5. 自举构建（Bootstrap）

### 5.1 环境

```powershell
# 官方 go 作为 bootstrap（GOROOT 指向官方工具链）
$env:GOROOT = "D:\ProgramFiles\DeveloperToolKit\Golang\go\go"
$env:PATH   = "$env:GOROOT\bin;" + $env:PATH
# 在 src 目录执行
cmd /c "make.bat"
```

### 5.2 关键陷阱

1. **GOCACHE 必须干净**：换工具链后不清缓存会导致 reloc 枚举错位，链接报
   `unknown reloc to ... : 105 (RelocType(105))`。务必：
   ```powershell
   go clean -cache
   ```
   或显式指向全新目录：`$env:GOCACHE = "<全新目录>"`。
2. **开发目录与发布目录要分别构建**：`D:\Projects\c\ohos-go` 和
   `D:\ProgramFiles\DeveloperToolKit\Golang\go\versions\ohos-go` 各自跑 make.bat。
3. **stringer 生成**：改 reloctype 后需用 stringer 重新生成 `reloctype_string.go`。
   仓库 GOROOT 未构建时 stringer 不可用，需在临时 module 中运行再拷回：
   ```bash
   # 临时目录建 module，go get 对应版本 stringer
   # 用 go:generate 同款参数生成
   ```
4. PowerShell 写文件编码陷阱：`Out-File`/`>` 会产生 BOM/CRLF，破坏 gofmt。
   用 `git checkout --theirs` 或 `[System.IO.File]::WriteAllText`（UTF8 无 BOM + LF）。

---

## 6. 本次升级踩过的具体坑（可复用）

### 6.1 arm64 asm7.go case 编号冲突

上游 1.26.5 新增 `case 108`（bti），OHOS 的 TLS_GD 也用了 108 → 编译报 duplicate case。
解决：把 OHOS 的 TLS_GD 改到空闲编号（109），optab 和 asmout 两处都要改。

### 6.2 getGodebugEarly 返回签名变化

上游把 `getGodebugEarly() string` 改成 `(string, bool)`。OHOS musl hunk 需适配：
`return value, true` / `return env, false`。

### 6.3 loader.go sizeFixups 双重循环

OHOS 曾在 1.24.5 里手动加过 sizeFixups 循环；1.26.5 也原生加了。合并后两个循环都在，
导致 `cloneToExternal` 对已 external 符号 panic。
解决：删掉 OHOS 的重复循环，保留上游那个（在 loadObjRefs 之后）。

### 6.4 cmd/go 的 goos 变量

OHOS 在 `go_test.go` 定义了 `goos`（= "openharmony" when IsOpenharmony），`script_test.go`
引用它。合并时 `go_test.go` 的 OHOS hunk 容易丢，导致 `undefined: goos`。
解决：恢复 `go_test.go` 中 `goos` 声明与赋值。

### 6.5 运行时崩溃：getGodebugEarly 在 mallocinit 前分配

这是**最严重**的运行时问题（`.so` 加载即 SIGSEGV）：

- 1.24.5：`getGodebugEarly()` 在 `mallocinit()` **之后**调用，OHOS musl hunk 读 `/proc/self/environ`（需分配内存）没问题。
- 1.26.5：把 `getGodebugEarly()` 移到了 `mallocinit()` **之前**，OHOS musl hunk 的
  `append`/`make`/`gostring` 在分配器初始化前执行 → 空指针崩溃。

解决：改为扫描 `libpreinit` 捕获的 C `environ` 指针（`muslEnviron`），全程不分配，
并剥离 `GODEBUG=` 前缀与上游一致。

### 6.6 发布目录缺工具链

`versions\ohos-go`（合并后的 main）只有源码，没有 `pkg/` 编译产物，`bin/` 下没有 go.exe。
直接用它会报错。必须先 `make.bat` 构建。

### 6.7 刷新 macOS 的已装工具链（`~/go1.27.1-ohos`）

`~/go1.27.1-ohos` 是下游（v2rayHM 的 `scripts/build_*.sh` 默认 `OHOS_GO_ROOT`）实际使用的
工具链。它不是 git 仓库，只是本仓库工作区的拷贝 + 一次 `make.bash`。源码改动后要这样刷：

```bash
cd ~/projects/ohos-go
rsync -a --delete \
  --exclude '.git/' --exclude '/bin/' --exclude '/pkg/' \
  --exclude 'src/cmd/dist/dist' --exclude '.DS_Store' \
  --exclude '.claude/' --exclude 'CLAUDE.md' --exclude 'resume.sh' \
  --exclude 'ohos-test-results*' \
  --exclude 'misc/openharmony/loopbackhap/.hvigor/' \
  --exclude 'misc/openharmony/loopbackhap/entry/build/' \
  --exclude 'misc/openharmony/loopbackhap/local.properties' \
  ./ ~/go1.27.1-ohos/
cd ~/go1.27.1-ohos && rm -rf bin pkg
cd src && GOTOOLCHAIN=local GOPROXY=off \
  GOROOT_BOOTSTRAP=/opt/homebrew/opt/go/libexec ./make.bash
cd ../misc && ../bin/go build -trimpath -o ../bin/go_openharmony_arm64_exec ./go_openharmony_exec
cp ../bin/go_openharmony_arm64_exec ../bin/go_openharmony_amd64_exec
```

排除项各有原因：

- **`/bin/`、`/pkg/`**：构建产物，交给 `make.bash` 自己维护。但**排除了不等于不用管** ——
  这两棵子树里是上一版的产物，而 `make.bash` 是增量的，所以下面那条 `rm -rf` 不能少。
  > **前导斜杠是必须的（2026-09-23 实测）。** 不锚定的话 `pkg/` 匹配**任意层级**的 `pkg` 目录，
  > 而树里有两个是上游签入的**测试数据**：`src/cmd/api/testdata/src/pkg`（10 个文件）与
  > `src/simd/testdata/pkg`（1 个）。写了 `'pkg/'` 它们就被静默丢掉 —— 11 个上游文件，
  > 在发布树里不存在。`bin/` 目前只有根目录那一处，但一并锚定，免得下一个 `testdata/bin/`
  > 再咬一次。
  > 实测判据（两种写法跑一遍就看出差别）：
  > `rsync -a --dry-run --itemize-changes --exclude 'pkg/' ./ /tmp/probe/ | grep -c cmd/api/testdata/src/pkg`
  > —— `'pkg/'` 得 0，`'/pkg/'` 得 16。
- **`ohos-test-results*`**：全量测试的落盘目录。它不是源码，`--delete` 会顺手删掉，
  所以显式豁免（rsync 的 `--delete` 不会删被排除的路径）。
- **`loopbackhap/.hvigor/`、`loopbackhap/entry/build/`、`loopbackhap/local.properties`**：
  DevEco 自己的文件。在仓库里由夹具的 `.gitignore` 挡着，但 **rsync 不读 `.gitignore`**，
  不排就会被拷进发布树。`local.properties` 里是**打包机器上 DevEco SDK 的绝对路径**
  （一行 `sdk.dir=/Applications/...`），对别人既没用又碍事。
  > **`--exclude` 只挡「拷进去」，不挡「已经在那儿」。** `--delete` 默认不清被排除的路径
  > （排除项是受保护的），所以上一次同步漏进去的那份**会变成钉子户**：2026-09-23 实测，
  > 排除项加好之后 `local.properties` 照样在装好的树里，得手工 `rm` 一次。
  > 判据是打包前 `tar tzf ... | grep local.properties`。
  > （`--delete-excluded` 能根治，但它会连 `ohos-test-results*` 一起删 —— 那是数据，别用。）
- **`.claude/`、`CLAUDE.md`、`resume.sh`**：本仓库的开发脚手架，不属于 Go 发行版。

> **`go.env` 已从排除列表里去掉**（2026-09-23）。以前必须排：只有已装树那份是手改的
> `GOTOOLCHAIN=local`，源码树还是上游的 `auto`，同步会把它冲掉。现在源码树也是 `local`
> （roadmap ⑧，`.github/workflows/ohos.yml` 里有断言守着），两棵树逐字相同 ——
> 让仓库当唯一事实源比两处手工维护省事。

> **`rm -rf bin pkg` 不能省。** `make.bash` 是增量的，只重编它发现的变更；上一版留下的、
> **没带 release 标志**的工具二进制会被原样留下，而它每次退出码都是 0。判据见 §6.8。

`-exec` 包装仍需手装：普通 `make.bash` 的 `goos == gohostos`，`wrapperPathFor` 返回空（见 §4.2）。

> **手装这条命令里的 `-trimpath` 不能省（2026-09-23 定案，beta2 就是在这儿翻的车）。**
> 这是 `bin/` 里**唯一**由手工命令产出、因而不吃 `GOFLAGS` release 标志的二进制
> （`cmd/dist` 里的那份在 `build.go:1708`，自带 `-trimpath`）。少了它，包装是整棵发布树里
> 唯一嵌着构建机绝对路径的文件，两个后果：
> 1. tar 包里泄出构建目录；
> 2. 包装内的 `runtime.GOROOT()` 是**构建机**的路径，而 `findGoroot`
>    （`misc/go_openharmony_exec/main.go`）优先信包装自身位置、只把 `runtime.GOROOT()`
>    当兜底 —— 于是解包到别处的树拿到的 host GOROOT 是错的，
>    `deviceCwdFor` 算出跨目录的 `../..`，退化成 `pushSourceTree`（只镜像包目录），
>    所有 `../../testdata/...` 读操作 ENOENT。**症状是 12 个包在设备上假红，看起来像 port 缺陷。**
> 现在有 `TestGorootFromExecutable` 钉住「自身位置优先」这条规则，但那测的是**规则**，
> 树的属性只能靠下面这条检查。

手装完 **必须查「树里没有构建机路径」**（`bin/go` 为 0 不代表整棵 `bin/` 为 0 —— 这正是 CI
只查 `bin/go` 时漏掉它的原因）：

```bash
cd ~/go1.27.1-ohos
for f in bin/*; do [ -f "$f" ] || continue
  printf '%6s  %s\n' "$(strings "$f" | grep -cE '^/[^ ]*\.go$')" "$f"
done        # 每一行都必须是 0
```

**判据要用上面这条「绝对路径形态的 `.go` 串」，不要用 `grep -cF "$PWD"`**（2026-09-23 订正）：
后者只在**站在构建树自己的路径上**时有效。解包到别处之后 `$PWD` 不是构建路径，计数
**恒为 0** —— 实测拿**beta2 首版 darwin 资产**跑那种写法得 `0`（看着干净），
换上面这条得 `174`（缺陷现行）。改后的判据路径无关，所以它**同时能用来看别人给的包**，
这也是「拿到别人的包先验一下」的入口。取证见测试方案 §5.8。
（那份首版资产 2026-09-23 已同名重打成修好的版本，这里引用的是留档的取证。）

> **顺带一条同类的存在性检查**（上游有、同步后没有 —— `git diff` 和 rsync 都看不见）：
> ```bash
> cd ~/projects/ohos-go
> prune=( -path ./.git -prune -o -path ./pkg -prune -o -path ./bin -prune -o
>         -path ./.claude -prune -o -name CLAUDE.md -prune -o -name resume.sh -prune -o
>         -name 'ohos-test-results*' -prune -o -name .DS_Store -prune -o
>         -path './misc/openharmony/loopbackhap/.hvigor' -prune -o
>         -path './misc/openharmony/loopbackhap/entry/build' -prune -o
>         -name local.properties -prune -o -type f -print )
> diff <(find . "${prune[@]}" | LC_ALL=C sort) \
>      <(cd ~/go1.27.1-ohos && find . "${prune[@]}" | LC_ALL=C sort)
> ```
> **空输出才算过。** 上面那条锚定 `pkg/` 的坑就是它抓出来的那类问题（11 个上游 testdata）。
> 必须把 rsync 的排除项原样再排一遍 —— 不排就报 261 行，全是**故意不同步**的东西
> （`.hvigor/`、`entry/build/`、`CLAUDE.md`…），等于没有这条检查。
> 2026-09-23 实测：不发散的树上输出为空；从装好的树里删掉一个 `golden.txt` 会打
> `325d324 < ./src/cmd/api/testdata/src/pkg/issue79145/golden.txt`。

刷新后的三连验收：

```bash
~/go1.27.1-ohos/bin/go version                       # go version go1.27.1-ohos darwin/arm64
cd ~/go1.27.1-ohos/src && ../bin/go tool dist test -run=misc:execwrapper
PATH="$HOME/go1.27.1-ohos/bin:$PATH" GOOS=openharmony GOARCH=arm64 ../bin/go test strings
```

> **这条验收不会污染发布构建 —— 已实测排除。** 第二条的 `dist test` 会按 `toolenv()`
> 重装一遍工具链（`build.go:1404` 那份 release 标志就是给它用的），第三条会编出目标架构
> 工具链（在 `bin/openharmony_*`、`pkg/tool/openharmony_*`，打包时排掉）。
> 2026-09-23 实测：刚 `make.bash` 完（阶段 A）与跑完 `dist test`（阶段 B）逐字节相同
> （`bin/go` 16069938、`gofmt` 2972978、`compile` 27078882，绝对路径计数均为 0）。
> 所以验收顺序不必躲 —— **树变「非 release」的来源只有 §6.8 那条增量 `make.bash`
> 留旧产物**，别把两件事混起来查。

### 6.8 打发布 tarball

发布物是**整棵安装树**（§6.7 那一棵），不是源码包 —— 下游要的是能直接换 `OHOS_GO_ROOT` 的东西。

**打之前必须先验「发布构建标志」真的在树里。** `cmd/dist` 有个 release 闸门
（`build.go:1399-1405`）：`isRelease || GO_BUILDER_NAME != ""` 时注入
`GOFLAGS=-trimpath -ldflags=-w -gcflags=cmd/...=-dwarf=false`。`isRelease`（`build.go:279`）
判的是 goversion 以 `release.` 或 `go` 开头且不含 `devel` —— 所以 `VERSION` 是 `go1.27.1-ohos`
（fork 的版本串，`VERSION` 第 1 行，见 roadmap ③）时**照样成立**，**但只在真正重新编译各工具时生效**。

> **版本串的后缀是有雷区的**：`-ohos` 安全，但换成含 `beta` 的会让 `cmd/api` 翻成开发版语义
> （`cmd/api/main_test.go:110`），含 `devel` 会让 `isRelease` 变假、`findgoversion` 转去走 git tag 路径、
> `-V=full` 开始附 buildID，写成 `+ohos` 或 `go1.27.1ohos` 则 `go/version` 直接判无效。
> 逐条实测见 roadmap ③。

> **踩过的坑：增量 `make.bash` 会把「没有 release 标志」的工具二进制原样留下。**
> 症状是 `bin/go` 里嵌着绝对路径、`compile` 带 DWARF（36.2 MB vs 正常的 27.1 MB），
> 而 `make.bash` 每次退出码都是 0。**这不是路径太长之类的无害差异** ——
> `-trimpath` 是发布构建的硬要求。
> 判据与修法：**整棵 `bin/` 顶层每个文件**的「绝对路径形态 `.go` 串」计数**都必须是 0**（循环见 §6.7；
> 只查 `bin/go` 会漏掉手装的 `_exec` 包装 —— 2026-09-23 就是这么漏的，见已知问题 7c；
> 而 `grep -cF "$PWD"` 那种写法在解包后的树上恒为 0，同样看不见 —— 两处都踩过）；
> 且 `pkg/tool/darwin_arm64/{compile,link,asm,cgo}` 的 sha256 应与本仓库的一致。
> 不符就 **`rm -rf bin pkg` 后重跑 `make.bash`**（强制全量，别指望增量自己发现）。

命名 `<tag>-<宿主>`：`go1.27.1-ohos-beta2-darwin-arm64.tar.gz` / `...-linux-amd64.tar.gz`。
**刻意不与官方 `go1.27.1.*` 同名**，免得装串。

从 §6.7 刷出来的安装树打（推荐，成员名天然就是 `go1.27.1-ohos`，且开发脚手架已被 rsync 排掉）：

```bash
cd ~ && tar czf /tmp/go1.27.1-ohos-beta2-darwin-arm64.tar.gz \
  --exclude='go1.27.1-ohos/ohos-test-results*' \
  --exclude='go1.27.1-ohos/bin/openharmony_*' \
  --exclude='go1.27.1-ohos/pkg/tool/openharmony_*' \
  --exclude='.DS_Store' \
  go1.27.1-ohos
(cd /tmp && shasum -a 256 go1.27.1-ohos-beta2-darwin-arm64.tar.gz \
  | tee go1.27.1-ohos-beta2-darwin-arm64.tar.gz.sha256)
```

> **摘要必须在包的目录里用「相对文件名」生成**（2026-09-23 实测踩到）：写绝对路径的话
> `.sha256` 里存的是 `/tmp/...` 或 `/opt/...`，而 release notes 让用户跑的是
> `shasum -a 256 -c <那个文件>` —— 在**用户自己的下载目录**里，那个绝对路径不存在，
> 于是校验报 `FAILED open or read`。首版 beta2 的两份 `.sha256` 是相对名（105/106 字节），
> 这是“顺手一敲就错、错了还看不出”的那类形状问题：**校验文件里只能有文件名**。

> **那两个 `openharmony_*` 排除项是 2026-09-23 补的，别删。** 在装好的树上跑过设备测试之后，
> 树里会多出 `bin/openharmony_arm64/` 与 `pkg/tool/openharmony_arm64/` —— 那是 `-exec` 包装
> **自己按需编出来的目标架构工具链**（`misc/go_openharmony_exec/main.go:239`，日志里那句
> `building the openharmony/arm64 toolchain (one time)`），**是缓存不是发行内容**：
> 它 `Stat` 一下不存在就现编，没有也照常工作。
> 不带这两条的话包里会多约 200 MB（实测 69 MiB → 247 MB），而**条目数只多 30 来条** ——
> 只看 `tar tzf | wc -l` 是发现不了的，必须看字节数。

**直接从源码树打（Linux 上常见，省一次 285 MB 的 rsync）**：树本身还带着 `.claude/`、
`CLAUDE.md`、`resume.sh` 这些开发脚手架，**必须自己排**；且目录名是 `ohos-go` 不是
`go1.27.1-ohos`，要用 GNU tar 的 `--transform` 改成员名（BSD tar 无此参数）：

> **这条配方只负责打包，不负责装 `_exec` 包装**（2026-09-24 补）：那一步在 Linux 上必须
> 先在源树里做一遍，§6.7 的后半段照抄即可（`../bin/go build -trimpath -o ../bin/go_openharmony_arm64_exec
> ./go_openharmony_exec`，再 `cp` 成 `_amd64_exec`）。**少了它包照样能编能跑** ——
> 两个 Go 二进制、`pkg/tool` 全都正常，只是下游的 `go test` 再也不会落到设备上，
> 失败方式是完全静默的。两份已发的 Linux 资产都带这 2 个成员（实测），
> 原因是打它们的那棵树先前已经装过；**换一棵新树就会漏**。验收清单第 3 条就是查它。

```bash
# 这台没有 / 上的空间，tar 落到 /opt（同一台机器上 df 挑大的那个盘）
cd /root/ohos-go && tar czf /opt/go1.27.1-ohos-beta2-linux-amd64.tar.gz \
  --transform='s,^\.$,go1.27.1-ohos,' --transform='s,^\./,go1.27.1-ohos/,' \
  --exclude='./.git' --exclude='./.claude' --exclude='./CLAUDE.md' --exclude='./resume.sh' \
  --exclude='./ohos-test-results*' --exclude='.DS_Store' \
  --exclude='./bin/openharmony_*' --exclude='./pkg/tool/openharmony_*' \
  --exclude='./misc/openharmony/loopbackhap/.hvigor' \
  --exclude='./misc/openharmony/loopbackhap/entry/build' \
  --exclude='./misc/openharmony/loopbackhap/local.properties' \
  .
(cd /opt && sha256sum go1.27.1-ohos-beta2-linux-amd64.tar.gz \
  | tee go1.27.1-ohos-beta2-linux-amd64.tar.gz.sha256)   # 相对名，同上
```

> `--transform` 那条**必须拆成两个**：只写 `^\./` 的话，`tar ... .` 产生的那个裸 `.`
> 成员匹配不上，包里会多一条 `./` 成员（无害，但与另一份包形状不一致，比对时会误判）。

- **解包后必须落到 `go1.27.1-ohos/`**（`tar` 的成员名就是它）。下游默认
  `OHOS_GO_ROOT=$HOME/go1.27.1-ohos`，改名字等于多一步配置。
- **`ohos-test-results*` 是唯一必须排的东西**（本仓库跑全量测试的落盘目录，约 29 MB ×N轮）。
  `.DS_Store` 是习惯性排除。**`pkg/openharmony_arm64_dynlink` 不用排** ——
  那只是 `-buildmode=shared` 能力测试的宿主产物，本树里根本没有（`pkg/` 只有 `include` 与 `tool`）。
- **包体约 70 MiB，条目约 17.5k。** 明显更大就是排漏了，**而且要先看字节数、别先看条目数** ——
  目标架构工具链缓存那 200 MB 只对应三十几条条目，`wc -l` 完全看不出来。
  `v1.27.1-ohos-darwin-arm64` 实测 69 MiB / 17466 条（`v1.27.1-beta2` 是 17463：多出的 3 条
  就是那轮新增的两个包装测试与一份发布说明）。**这个数只随新增源码文件增长**，
  所以它是一条粗判据 —— 真正能定性的是字节数那一条。

打完的验收（**这一步不能省，它验的是「交出去的那个文件」而不是「你本地那棵树」**）：

```bash
F=<tarball>
# 1. 成员名。词汇表要和打包命令里的 --exclude 对齐 —— 只写几个显眼的词，
#    「必须为空」证明的只是那几个词没进包，不是排干净了。
tar tzf $F | grep -E 'ohos-test-results|\.DS_Store|\.hvigor|resume\.sh|^[^/]+/\.git/|local\.properties|/entry/build/'  # 必须为空
tar tzf $F | wc -l                                        # ≈17470（只随新增源码文件增长）；暴增说明排漏了
# 2. 字节数。这才是看得见「目标架构工具链缓存」的那把尺（见本节开头）。
ls -lh $F                                                 # 69M；明显更大就是排漏了
# 3. 两个 _exec 包装必须在包里。少了它们包仍能编能跑，只是下游的 go test
#    再也不会落到设备上 —— 失败方式是完全静默的。
tar tzf $F | grep -c 'go_openharmony_.*_exec$'            # 必须是 2
mkdir -p /tmp/accept && tar xzf $F -C /tmp/accept
A=/tmp/accept/go1.27.1-ohos
$A/bin/go version                                         # 必须带 -ohos
# 4. 整棵 bin/ 里没有构建机路径。判据是路径无关那条（§6.7 已否掉 grep "$(pwd)"：
#    解包到别处之后它恒为 0），且要遍历整个 bin/ —— 只查 bin/go 会漏掉手装的包装。
for f in $A/bin/*; do [ -f "$f" ] || continue
  printf '%6s  %s\n' "$(strings "$f" | grep -cE '^/[^ ]*\.go$')" "$f"
done        # 每一行都必须是 0
$A/bin/go tool dist list | grep openharmony               # 两个目标都在
# 5. 摘要要真跑一次 -c。相对名的摘要在别的目录里连文件都找不到，会静默地「没验」。
(cd "$(dirname $F)" && shasum -a 256 -c "$(basename $F).sha256")   # 必须 OK
```

**再解出一棵新树、用它编一个产物**（只跑 `go version` 会漏掉「`pkg/tool` 没打进去」这类缺陷）：
一定要**从这个 tarball 里解出来的树**编，不是从本地那棵编。

```bash
export PATH=$A/bin:$PATH GOCACHE=/tmp/accept/.cache
mkdir -p /tmp/accept/work && cd /tmp/accept/work
printf 'package main\nimport ("fmt";"net";"os")\nfunc main(){c,_:=net.Dial("tcp","1.1.1.1:53");fmt.Println(os.Args[0],c)}\n' > main.go
# arm64：纯 Go 就够 —— 默认 PIE 会自己带上 musl 解释器，不需要 SDK
GOOS=openharmony GOARCH=arm64 CGO_ENABLED=0 go build -o app main.go
readelf -l app | grep INTERP     # → /lib/ld-musl-aarch64.so.1
# amd64：纯 Go 必须失败，且必须是这一句（这是 7b 修复的断言，不是意外）
GOOS=openharmony GOARCH=amd64 CGO_ENABLED=0 go build -o app64 main.go 2>&1 \
  | grep -q 'requires external (cgo) linking' && echo "7b OK"
```

- **amd64 的「能编出产物」这一条在没装 OHOS SDK 的机器上验不了**（强制外部链接 ⇒ 要 clang）。
  所以拿 238（Linux，无 SDK）做验收时，覆盖面是「arm64 产物正确 + amd64 报错正确」，
  真正的 amd64 产物形状在 macOS 那份上用 `-buildmode=c-shared` 验（见 §4.3）。
- `readelf` 在 macOS 上不自带，用 `$OHOS_SDK/native/llvm/bin/llvm-readelf`。

---

## 7. 验证清单

```powershell
# 冲突标记清零
git grep -n -e '<<<<<<< HEAD' -e '>>>>>>> <tag>' -- .

# 无未合并/未暂存
git diff --name-only --diff-filter=U
git diff --name-only

# gofmt（排除 testdata 与 test/ 目录，它们故意不格式化）
gofmt -l <staged .go files>

# go vet
go vet cmd/link/internal/ld cmd/link/internal/loader cmd/internal/obj/x86 cmd/internal/obj/arm64 cmd/go/internal/work internal/platform

# 关键测试
go test cmd/internal/obj cmd/internal/obj/arm64 cmd/internal/objabi cmd/link cmd/go/internal/work
go test internal/platform internal/testenv
go test os os/user net/url crypto/x509 encoding/pem encoding/asn1 os/exec go/build testing
go test runtime   # 耗时较长（~4 分钟）
```

### OHOS 行为验证

```bash
# 1) OHOS c-shared 交叉编译（x86_64 + arm64）
#    用 OHOS SDK clang + sysroot
GOOS=openharmony GOARCH=amd64 CGO_ENABLED=1 \
CC="<sdk>/llvm/bin/clang.exe --target=x86_64-linux-ohos --sysroot=<sdk>/sysroot -D__MUSL__" \
AR="<sdk>/llvm/bin/llvm-ar.exe" \
go build -buildmode=c-shared -o out.so .

# 2) 确认 delta 保留
git diff <上游tag> -- src/runtime/os_linux.go   # 应只剩 OHOS musl hunks
git diff <上游tag> -- src/internal/platform/zosarch.go  # openharmony 条目在

# 3) 真机/模拟器运行（flutter）
fvm flutter run -d 127.0.0.1:5555   # 需 GOROOT 指向 OHOS 工具链

# 4) 能力验收夹具（misc/openharmony/，结论见 §4.2）
SDK=<sdk>; LLVM=$SDK/native/llvm/bin
export OHOS_HDC=$SDK/toolchains/hdc OHOS_TARGET=127.0.0.1:5555
export CC="$LLVM/clang --target=aarch64-linux-ohos --sysroot=$SDK/native/sysroot -D__MUSL__"
export AR="$LLVM/llvm-ar"                 # 必须：macOS 的 /usr/bin/ar 拒收 ELF 成员
export GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1 GOTOOLCHAIN=local

cd misc                                   # 夹具在 misc 模块里，必须在 misc 下构建
../bin/go build -o /tmp/probe ./openharmony/probe && ./openharmony/ohosrun /tmp/probe
../bin/go build -buildmode=c-shared  -o /tmp/libadd.so ./openharmony/cshared
../bin/go build -buildmode=c-archive -o /tmp/libadd.a  ./openharmony/cshared
../bin/go build -buildmode=plugin    -o /tmp/plug.so   ./openharmony/plug
../bin/go build -asan -buildmode=pie -o /tmp/asandemo  ./openharmony/asandemo
```

`probe` 是零依赖的事实采集器（split identity、VMA 宽度、CA 库位置、DNS），**升级后先跑它** ——
`vma_bits_*` 直接决定 `-race` 能不能开门。其余目录各自对应 §4.2 表格里的一行能力：
`cshared`+`cdriver`（c-archive/c-shared，驱动怎么链见 `cdriver/main.c` 的文件头）、
`plug`+`hostplug`、`libadd`+`hostshared`、`asandemo`、`cgomin`（PIE 最小复现）。
`CGO_ENABLED=0` 的 `probe` 不需要 `CC`。

---

## 8. 提交与发布

### 8.1 提交

```bash
git add <resolved files>
git commit -m "Merge tag 'go1.26.5' into ohos-1.24-base"
git push origin ohos-1.24-base
```

### 8.2 建 PR（关键：历史桥接）

`main` 分支与开发分支无共同历史，GitHub 拒绝建 PR（"no history in common"）。
必须先在分支上造一个把 main 当祖先、但树保持干净的 merge 提交：

```bash
# 1) 造 merge 提交：tree 用干净分支树，parents = (分支, main)
$tree = git rev-parse '<分支>^{tree}'
$p1   = git rev-parse <分支>
$p2   = git rev-parse origin/main
$new  = git commit-tree $tree -p $p1 -p $p2 -m "Merge remote-tracking branch 'origin/main' into <分支>"
git reset --hard $new

# 2) 推送 + 建 PR（gh 需指定仓库，避免认错仓库）
git push origin <分支>
gh pr create -R star4277/ohos-go --base main --head <分支> --title "..." --body-file body.md
```

注意：
- 不要用 `git merge -X ours origin/main`（会重新加回上游删除的旧文件，污染树）。
- 正确做法是 commit-tree 保持树不变，仅补 parent。
- `gh` 必须在目标仓库上下文（`-R`），否则可能操作到别的仓库（如 golang/go）。

---

## 9. 环境参考（Windows）

| 项 | 路径 |
|---|---|
| 官方 go（bootstrap） | `D:\ProgramFiles\DeveloperToolKit\Golang\go\go` |
| 发布目录（合并后 main） | `D:\ProgramFiles\DeveloperToolKit\Golang\go\versions\ohos-go` |
| OHOS SDK | `D:\ProgramFiles\DeveloperToolKit\Jetbrains\Huawei\DevEcoStudio\sdk\default\openharmony\native` |
| OHOS 模拟器地址 | `127.0.0.1:5555` |
| 开发目录 | `D:\Projects\c\ohos-go` |
| clash_ui 项目 | `D:\Projects\clash_ui`（`go\` 是 Go 模块，`go_builder\ohos` 是插件） |
