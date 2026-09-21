---
name: merge-upstream
description: Merge a newer upstream Go release tag into the OpenHarmony branch of this Go toolchain fork. Covers pre-flight (upstream remote + tag), OHOS delta extraction, conflict classification, the residual-diff verification gate, the OHOS feature checklist, bootstrap build traps, and PR history bridging into main. Use when the user says "升级 Go", "合并 go1.2X.Y", "merge upstream tag", "rebase OHOS onto go1.27", or asks to bring a new Go release into this fork.
---

# Upstream Go 合并到 OHOS 分支

`docs/go-upgrade-guide.md` 是这份流程的完整散文版（含踩坑细节），本 skill 是它的可执行摘要。**开始前先读那份文档的 §3 和 §6。**

一次合并涉及 ~190 个文件、跨 compiler/linker/runtime 三层。不要靠猜冲突，靠**分类 + 事后 diff 校验**。

## 0. Pre-flight（本仓库当前状态有缺口，必须先补）

```bash
# upstream remote —— 本仓库默认没有，需要先加
git remote -v | grep -q upstream || git remote add upstream https://github.com/golang/go.git

# 上游 tag —— 本仓库是 blob:none 部分克隆，本地几乎没有上游 tag
git tag -l "go1.2*"                       # 只有 v1.24.5 / v1.26.5-beta1 这类 fork tag
git fetch upstream --tags                 # 首次需要；之后可按需 fetch 单个
git fetch upstream tag go1.27.0           # 只取一个 tag（更快）
```

确认三件事再动手：

1. `git rev-parse <旧上游tag>` 能解析（当前的 OHOS 基线）
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
| 纯上游文件（OHOS 没碰过） | `git checkout --theirs <file>` |
| OHOS 改过的文件 | 取 theirs（上游版本），再**手工重套** OHOS hunk |
| OHOS 新增文件（`*_openharmony.go`、`rt0_openharmony_*.s`） | 保留 |
| 上游已原生实现该改动 | 取 theirs，**丢弃 OHOS hunk**（见 guide §3.4） |

收尾清冲突标记：

```bash
git grep -n -e '<<<<<<< HEAD' -e '>>>>>>>' -- .   # 必须为空
git diff --name-only --diff-filter=U              # 必须为空
```

## 3. 校验闸门（不通过就不要往下走）

这是整个流程的核心断言：**残留 diff 必须恰好等于 OHOS delta，多一行少一行都说明搞错了。**

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
| TLS_GD reloc | `cmd/internal/objabi/reloctype.go` — `R_AMD64_TLS_GD` / `R_ARM64_TLS_GD` |
| amd64 TLS 描述符 | `cmd/internal/obj/x86/asm6.go` — `isOpenharmony && Flag_shared` |
| arm64 TLS_GD | `cmd/internal/obj/arm64/asm7.go` — **case 编号需避开上游新占用的号** |
| 平台登记 | `internal/platform/zosarch.go`、`supported.go` |
| goos 映射 | `cmd/go/internal/cfg/cfg.go`、`cmd/dist/build.go` |
| c-shared 入口 | `runtime/rt0_openharmony_amd64.s`、`rt0_openharmony_arm64.s` |
| 共享库 GC 数据 | `cmd/link/internal/ld/decodesym.go` — `decodetypeGcprogShlibByReloc` |
| 测试标签 | `testing/benchmark.go` 的 goos 打印、`crash_cgo_test.go` 的 openharmony case |
| 链接器跳过 | `cmd/link/link_test.go` — `\|\| runtime.IsOpenharmony` |
| ASan 平台登记 | `internal/platform/supported.go` — `ASanSupported` 的 openharmony case |
| 默认 PIE | `internal/platform/supported.go` — `DefaultPIE` 的 `"android", "ios", "openharmony"`。丢了它 cgo 可执行文件会 `Signal 11` |
| ELF 解释器 | `cmd/link/internal/ld/elf.go` — `case objabi.Hlinux` 里的 openharmony 分支，取 `LinuxdynldMusl` 而不是探测宿主。丢了它纯 Go 的 PIE 二进制在设备上 ENOENT |

合并后**必须在设备上重跑能力验收**（夹具 `misc/openharmony/`，结论表见
`docs/go-upgrade-guide.md` §4.2）。合并最容易悄悄打断的几条：

- **cgo 可执行文件依赖 `-buildmode=pie`**：默认 buildmode 下任何 `import "C"` 的程序在
  `init()` 前 `Signal 11`。上游若动了 `buildModeInit` 的 `codegenArg`/`ldBuildmode`，
  或 `obj/x86/asm6.go`、`obj/arm64/asm7.go` 里 `Flag_shared` 门控的 TLS 分支，先查这条。
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
./bin/go build -buildmode=c-shared -o out.so .
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
| gofmt 报奇怪错误 | PowerShell `>`/`Out-File` 写入了 BOM/CRLF → 用 `git checkout --theirs` 或 `[System.IO.File]::WriteAllText`（UTF8 无 BOM + LF） |

## 8. 发布

提交后推送。**注意 `main` 与开发分支无共同历史**，GitHub 会拒绝建 PR（"no history in common"），必须造一个「把 main 当祖先、但树保持干净」的 merge 提交：

```bash
tree=$(git rev-parse '<分支>^{tree}')
p1=$(git rev-parse <分支>)
p2=$(git rev-parse origin/main)
new=$(git commit-tree $tree -p $p1 -p $p2 -m "Merge remote-tracking branch 'origin/main' into <分支>")
git reset --hard $new
git push origin <分支>

gh pr create -R star4277/ohos-go --base main --head <分支> --title "..." --body-file body.md
```

两个坑：

- **不要**用 `git merge -X ours origin/main` —— 会把上游已删除的旧文件加回来污染树。正解是 `commit-tree` 保持 tree 不变、只补 parent。
- `gh` 必须带 `-R star4277/ohos-go`，否则可能操作到别的仓库（如 golang/go）。
