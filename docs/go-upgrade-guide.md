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
| `-race` | ❌ 不可用（不开门） | TSAN 的 arm64 `InitializePlatformEarly` 硬要求 48 位 VMA（`cmp #48` / `b.ne` → `unsupported VMA range`），而设备用户地址空间只有 39 位：ASan 自报 `HighMem [0x002000000000, 0x007fffffffff]`，探针 `vma_bits_stack=38`。TSAN shadow 在 32 TiB（45 位）处，够不着。开了只会把构建期一句清楚报错换成运行期 sanitizer 崩溃 |
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
  --exclude '.git/' --exclude 'bin/' --exclude 'pkg/' --exclude 'go.env' \
  --exclude 'src/cmd/dist/dist' --exclude '.DS_Store' \
  --exclude '.claude/' --exclude 'CLAUDE.md' --exclude 'resume.sh' \
  ./ ~/go1.27.1-ohos/
cd ~/go1.27.1-ohos/src && GOTOOLCHAIN=local GOPROXY=off \
  GOROOT_BOOTSTRAP=/opt/homebrew/opt/go/libexec ./make.bash
cd ../misc && ../bin/go build -o ../bin/go_openharmony_arm64_exec ./go_openharmony_exec
cp ../bin/go_openharmony_arm64_exec ../bin/go_openharmony_amd64_exec
```

三处必须排除，各有原因：

- **`go.env`**：发布树里是 `GOTOOLCHAIN=local`（外加一段说明），源码树是上游的 `auto`。
  反向覆盖会让这棵树有被自动换掉的风险 —— 而上游工具链编不出 `GOOS=openharmony`。
  没有任何 dist 代码会写这个文件，那个 `local` 是手工改的，同步时别冲掉。
- **`bin/`、`pkg/`**：构建产物，交给 `make.bash` 自己维护。
- **`.claude/`、`CLAUDE.md`、`resume.sh`**：本仓库的开发脚手架，不属于 Go 发行版。

`-exec` 包装仍需手装：普通 `make.bash` 的 `goos == gohostos`，`wrapperPathFor` 返回空（见 §4.2）。

刷新后的三连验收：

```bash
~/go1.27.1-ohos/bin/go version                       # go version go1.27.1-ohos darwin/arm64
cd ~/go1.27.1-ohos/src && ../bin/go tool dist test -run=misc:execwrapper
PATH="$HOME/go1.27.1-ohos/bin:$PATH" GOOS=openharmony GOARCH=arm64 ../bin/go test strings
```

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
> 判据与修法：`strings bin/go | grep -c '<树的绝对路径>'` **必须是 0**；
> 且 `pkg/tool/darwin_arm64/{compile,link,asm,cgo}` 的 sha256 应与本仓库的一致。
> 不符就 **`rm -rf bin pkg` 后重跑 `make.bash`**（强制全量，别指望增量自己发现）。

```bash
cd ~ && tar czf /tmp/go1.27.1-ohos-beta1-darwin-arm64.tar.gz \
  --exclude='go1.27.1-ohos/ohos-test-results' \
  --exclude='go1.27.1-ohos/ohos-test-results-*' \
  --exclude='.DS_Store' \
  go1.27.1-ohos
shasum -a 256 /tmp/go1.27.1-ohos-beta1-darwin-arm64.tar.gz \
  | tee /tmp/go1.27.1-ohos-beta1-darwin-arm64.tar.gz.sha256
```

- **解包后必须落到 `go1.27.1-ohos/`**（`tar` 的成员名就是它）。下游默认
  `OHOS_GO_ROOT=$HOME/go1.27.1-ohos`，改名字等于多一步配置。
- **`ohos-test-results*` 是唯一必须排的东西**（本仓库跑全量测试的落盘目录，约 29 MB）。
  `.DS_Store` 是习惯性排除。**`pkg/openharmony_arm64_dynlink` 不用排** ——
  那只是 `-buildmode=shared` 能力测试的宿主产物，本树里根本没有（`pkg/` 只有 `include` 与 `tool`）。
- 命名刻意**不与官方 `go1.27.1.*` 同名**，免得装串。

打完的验收（**这一步不能省，它验的是「交出去的那个文件」而不是「你本地那棵树」**）：

```bash
tar tzf <tarball> | grep -E 'ohos-test-results|\.DS_Store'    # 必须为空
cd /tmp && tar xzf <tarball> && /tmp/go1.27.1-ohos/bin/go version
# 再解出一棵新树、用它编一个 cgo 可执行文件，确认 PIE + musl 解释器：
readelf -l app | grep INTERP          # → /lib/ld-musl-aarch64.so.1
```

最后那条是关键：**新解出来的树必须自己就能产出可用产物** ——
只跑 `go version` 会漏掉「`pkg/tool` 没打进去」这类缺陷。

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
