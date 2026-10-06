---
name: merge-upstream
description: Merge a newer upstream Go release tag into the OpenHarmony branch of this Go toolchain fork. Covers pre-flight (upstream remote + tag), OHOS delta extraction, conflict classification, the residual-diff verification gate, the OHOS feature checklist, bootstrap build traps, and PR history bridging into main. Use when the user says "升级 Go", "合并 go1.2X.Y", "merge upstream tag", "rebase OHOS onto go1.27", or asks to bring a new Go release into this fork.
---

# Upstream Go 合并到 OHOS 分支

`docs/go-upgrade-guide.md` 是这份流程的完整散文版（含踩坑细节），本 skill 是它的可执行摘要。**开始前先读那份文档的 §3 和 §6。**

一次合并涉及 ~190 个文件、跨 compiler/linker/runtime 三层。不要靠猜冲突，靠**分类 + 事后 diff 校验**。

## 0. Pre-flight

```bash
# upstream remote —— 已配好（远端名 upstream → github.com/golang/go）；这行只是幂等保险
git remote -v | grep -q 'upstream.*golang/go' || git remote add upstream https://github.com/golang/go.git

# 上游 tag —— 本仓库是 blob:none 部分克隆，tag 要抓（当前本地已有 go1.27.0/.1 与 go1.27rc*）
git tag -l "go1.2*"
git fetch upstream --tags                 # 全量；重，一般用下面单抓
git fetch upstream tag go1.27.1           # 取单个 tag（更快）。注意上游主干是 master，不是 main
```

确认三件事再动手：

1. `git rev-parse <旧上游tag>` 能解析（当前的 OHOS 基线 —— **现在是 `go1.27.1`**）
2. `git rev-parse <新上游tag>` 能解析（目标）
3. 工作区干净 —— 合并中途夹带未提交改动会让 delta 校验失效

## 1. 提取 OHOS delta（最重要的一步）

先固定「OHOS 相对旧上游到底改了什么」，后面所有冲突都以它为唯一依据：

```bash
git diff <旧上游tag> <OHOS-HEAD> > /tmp/ohos-delta.patch
git diff --stat <旧上游tag> <OHOS-HEAD>          # 记住文件数/行数，结束时对账
```

混合冲突文件另存单份 patch，便于重套：

```bash
git diff <旧上游tag> <OHOS-HEAD> -- <file> > /tmp/ohos-$(echo <file> | tr / _).patch
```

## 2. 合并与冲突分类

```bash
git merge <新上游tag>
```

| 类别 | 处理 |
|---|---|
| 上游新增 / 未冲突的文件（OHOS 没碰过） | 无需处理 —— 合并自动采用上游版本（没碰过的文件**根本不会冲突**，不用 `checkout --theirs`） |
| OHOS 改过的文件 | 取 theirs（上游版本），再**手工重套** OHOS hunk |
| OHOS 新增文件（`*_openharmony.go`、`rt0_openharmony_*.s`） | 保留 |
| 上游已原生实现该改动 | 取 theirs，**丢弃 OHOS hunk**（见 guide §3.4） |

收尾清冲突标记：

```bash
git grep -n -e '<<<<<<< HEAD' -e '>>>>>>>' -- .   # 必须为空
git diff --name-only --diff-filter=U              # 必须为空
```

## 3. 校验闸门（不通过就不要往下走）

这是整个流程的核心断言：**残留 diff 必须等于 OHOS delta —— 文件集合相同，每一处都是我们的 hunk。**

（行数允许偏差：上游若改到了我们 hunk 的所在处（§7 那几类），重套后行数本就该变，此时看 hunk 语义，别死抠行号。）

```bash
git diff <新上游tag> HEAD --stat                  # 与第 1 步的 --stat 对账
git diff <新上游tag> -- src/runtime/os_linux.go   # 应只剩 OHOS musl hunks
git diff <新上游tag> -- src/internal/platform/zosarch.go   # openharmony 条目还在
```

某个文件的 diff 为**零** = 上游已原生实现，OHOS hunk 应删除（这是正确结果，不是漏改）。

## 4. OHOS 特性清单（逐项确认仍在）

| 特性 | 位置 |
|---|---|
| musl TLS 检测 | `runtime/os_linux.go` — `_cgo_is_musl` / `libmusl` / `libpreinit` |
| musl 环境变量（**不可分配内存**） | `runtime/runtime1.go` — `procEnviron` / `muslEnviron` / `goenvs_musl` |
| `IsOpenharmony` 常量 | `runtime/extern.go` — `const IsOpenharmony bool = goos.IsOpenharmony == 1`；下面多数行都依赖它 |
| **split identity 生成器** | `internal/goos/gengoos.go`（openharmony 特例）**及其生成的 `zgoos_*.go`** —— 改生成器、**别改生成物**；丢了它 `runtime.GOOS` 不再镜像成 `linux`，「是不是 Linux」的所有分支全崩 |
| TLS_GD reloc | `cmd/internal/objabi/reloctype.go` — `R_AMD64_TLS_GD` / `R_ARM64_TLS_GD` |
| amd64 TLS 描述符 | `cmd/internal/obj/x86/asm6.go` — `isOpenharmony && Flag_shared` |
| arm64 TLS_GD | `cmd/internal/obj/arm64/asm7.go` — **case 编号需避开上游新占用的号**（`optab` 与 `asmout` 都在 asm7.go，**两处一起挪**；`C_TLS_GD` 类常量在 `arm64/a.out.go`） |
| buildmode / TLS 模型 | `cmd/go/internal/work/init.go` 的 `buildModeInit` —— openharmony 走 `-shared`，arm64 与 amd64 都置 TLS 模型 `"GD"`。§4.2 那条 PIE 警告就落在这个函数 |
| **amd64 强制外链** | `internal/platform/supported.go` 的 `MustLinkExternal`（`case "openharmony"` → `goarch != "arm64"` 返回 true）**及 `cmd/dist/build.go` 的引导期副本**；`cmd/dist/build_test.go` 的 `TestMustLinkExternal` 逐格比对两者，漏一处就红。丢了它 amd64 **每次**默认构建都死在 `cannot handle R_AMD64_TLS_GD ... when linking internally` |
| 平台登记 | `internal/platform/zosarch.go`、`supported.go` |
| goos 映射 | `cmd/go/internal/cfg/cfg.go`、`cmd/dist/build.go` |
| c-shared 入口 | `runtime/rt0_openharmony_amd64.s`、`rt0_openharmony_arm64.s` |
| 共享库 GC 数据 | `cmd/link/internal/ld/decodesym.go` — `decodetypeGcprogShlibByReloc` |
| 测试标签 | `testing/benchmark.go` 的 goos 打印、`crash_cgo_test.go` 的 openharmony case |
| 链接器跳过 | `cmd/link/link_test.go` — `\|\| runtime.IsOpenharmony` |
| ASan 平台登记 | `internal/platform/supported.go` — `ASanSupported` 的 openharmony case |
| 默认 PIE | `internal/platform/supported.go` — `DefaultPIE` 的 `"android", "ios", "openharmony"`。丢了它 cgo 可执行文件会 `Signal 11` |
| ELF 解释器 | `cmd/link/internal/ld/elf.go` — `case objabi.Hlinux` 里的 openharmony 分支，取 `LinuxdynldMusl` 而不是探测宿主。丢了它纯 Go 的 PIE 二进制在设备上 ENOENT |
| 其它 stdlib 替换 | `net/interface_table_openharmony.go` + `net/interface_table_linux.go` + `net/cgo_unix_cgo.go`（`getifaddrs` 替 netlink）、`time/zoneinfo_openharmony.go`、`mime/type_openharmony.go` |
| `go.env` 的 `GOTOOLCHAIN=local` | 仓库根 `go.env` **逐字就是发出去的值**；上游是 `auto`，合并会被盖掉，且**症状只在下游**（会去下官方工具链，编不出 `openharmony`） |

合并后**必须在设备上重跑能力验收**（夹具 `misc/openharmony/`，结论表见
`docs/go-upgrade-guide.md` §4.2）。合并最容易悄悄打断的几条：

- **cgo 可执行文件回归成 `Signal 11`**：若合并丢了 `platform.DefaultPIE` 的 openharmony 分支
  （或动了 `buildModeInit` 的 `codegenArg`/`ldBuildmode`，或 `obj/x86/asm6.go`、`obj/arm64/asm7.go` 里
  `Flag_shared` 门控的 TLS 分支），默认 buildmode 下任何 `import "C"` 的程序就会在 `init()` 前
  `Signal 11`。**这是回归症状**：2026-09 修好 `DefaultPIE` 之前就是这状态，现在默认 buildmode 已直接可用。
- c-archive 在 macOS 上必须 `AR=$SDK/native/llvm/bin/llvm-ar`，否则静默产出 96 字节空库。
- `-race` **不要**开门（TSAN 要 48 位 VMA，设备只有 39 位）；`-asan` 可以，且要配 `pie`。

## 5. 构建

```bash
go clean -cache        # 必须！换工具链后缓存不干净 → reloc 枚举错位 → link 报
                       # "unknown reloc to ...: 105 (RelocType(105))"
cd src && ./make.bash
```

改了 `reloctype.go` → 必须重新生成 `reloctype_string.go`。树内 `stringer` 在工具链构建好之前不可用，需在临时 module 里 `go install` 对应版本再拷回。

## 6. 测试

```bash
../bin/go test cmd/internal/obj cmd/internal/obj/arm64 cmd/internal/objabi cmd/link cmd/go/internal/work
../bin/go test internal/platform internal/testenv
../bin/go test os os/user net/url crypto/x509 encoding/pem encoding/asn1 os/exec go/build testing
../bin/go test runtime        # ~4 分钟
```

gofmt 排除 `test/` 与 `testdata/`（它们故意不格式化）。

OHOS 行为实测：

```bash
OHOS_SDK=/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony
GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1 \
CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__" \
../bin/go build -buildmode=c-shared -o out.so .   # cwd = src/
```

**`CC` 必须写 SDK clang 的绝对路径。** 裸 `clang` 会解析到 `/usr/bin/clang`（Apple clang），它不认
`aarch64-linux-ohos`，报 `posix_spawn failed: No such file or directory`。**只有需要外部链接的包才会调
clang** —— 所以错的 `CC` 会伪装成「某个包坏了」，而不是「工具链设置错了」，很容易漏。

**同理，验证时要用对工具链**：跑之前先确认树是新的，别信目录名里的版本号 ——
`git show HEAD:src/internal/platform/supported.go | grep -A2 'func DefaultPIE'` 必须出现 `openharmony`。
装好的 `~/go1.27.1-ohos` 可能就是过期的（2026-09-21 就中过一次：缺默认 PIE，cgo 二进制上设备 `Signal 11`）。

## 7. 已知冲突模式（命中就直接照做，别重新分析）

| 症状 | 原因与解法 |
|---|---|
| arm64 `duplicate case` | 上游新增了与 `R_ARM64_TLS_GD` 相同的 case 号 → 挪到空闲编号，`optab` 和 `asmout` 两处都改 |
| `getGodebugEarly` 编译错 | 上游签名变为 `(string, bool)` → OHOS hunk 适配 `return value, true` |
| link 时 `cloneToExternal` panic | 上游和 OHOS 各加了一份 `sizeFixups` 循环 → 删 OHOS 那份，留上游的 |
| `undefined: goos` | `cmd/go/go_test.go` 的 OHOS `goos` 声明在合并中丢失 → 恢复它 |
| `.so` 加载即 SIGSEGV | `getGodebugEarly` 被上游移到 `mallocinit` 之前 → musl 路径必须全程不分配内存，改读 `libpreinit` 捕获的 C `environ` |
| gofmt 报奇怪错误 | 文件被写成了 BOM/CRLF（历史触发者是 Windows 的 PowerShell `>`/`Out-File`；现宿主是 macOS，多为工具链进了 CRLF）→ `git checkout --theirs` 重取，或任何能保证 UTF-8 无 BOM + LF 的方式重写 |

## 8. 发布

合并完成后直接推 `origin/main` —— **2026-09-25 起主干开发已在 `main`**（默认分支，
CI 触发器 `['main','ohos-*']`；`ohos-1.27-base` 是 `main` 的祖先，保留为历史）：

```bash
git push origin main
```

> **历史遗留（已不适用）**：早期 `main` 是一棵 vintage OHOS 树，与开发分支**没有共同历史**，
> GitHub 会拒建 PR（"no history in common"），当时必须在分支上用 `git commit-tree` 造一个
> 「tree 保持干净、只把 `main` 补成第二个 parent」的 merge 提交才能建 PR（别用
> `git merge -X ours origin/main` —— 那会把上游已删的旧文件加回来污染树）。`main` 换成正常
> 历史后这条已作废；仅当将来又出现「无共同历史」的分支时才需要，配方见
> `docs/go-upgrade-guide.md` §8.2。
