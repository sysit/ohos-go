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

### 3.4 曾被子sumed 的 OHOS 改动（上游已原生实现，直接取 theirs）

- `doc/godebug.md` + `godebugs/table.go`（httpcookiemaxnum）
- `crypto/x509/parser_test.go`、`verify_test.go`、`verify.go`（约束匹配移到泛型 constraints.go，取代 OHOS reversedDomainsCache 优化）
- `encoding/pem/pem.go`（OHOS 加的 `getLine` 上游已有）
- `os/exec/lp_plan9.go`、`lp_windows.go`（lookPath 重构上游已含 validateLookPath）
- `runtime/runtime2.go`（allpSnapshot 上游已原生）
- `net/url/url.go`、`url_test.go`
- `runtime/crash_cgo_test.go` 的大部（1.26.5 重构为 race.Enabled 跳过）

---

## 4. OHOS 核心特性清单（升级时必须保留）

升级后务必逐项核对以下能力仍在：

| 特性 | 关键位置 |
|---|---|
| musl TLS 检测 | `runtime/os_linux.go` 的 `_cgo_is_musl` / `libmusl` / `libpreinit` |
| musl 环境变量 | `runtime1.go` 的 `procEnviron` / `muslEnviron` / `goenvs_musl` / `readNullTerminatedStringsFromFile` |
| TLS_GD reloc | `objabi/reloctype.go` 的 `R_AMD64_TLS_GD`、`R_ARM64_TLS_GD`（编号靠后追加） |
| amd64 TLS 描述符 | `cmd/internal/obj/x86/asm6.go`（`ctxt.Tls=="GD" || (isOpenharmony && Flag_shared)`） |
| arm64 TLS_GD | `cmd/internal/obj/arm64/asm7.go`（case 编号需避开上游已占用的号） |
| openharmony 平台登记 | `internal/platform/zosarch.go`、`supported.go` |
| goos 映射 | `cmd/go/internal/cfg/cfg.go`、`cmd/go/go_test.go` 的 `goos`、cmd/dist |
| c-shared 入口 | `runtime/rt0_openharmony_amd64.s`、`rt0_openharmony_arm64.s` |
| 共享库 GC 数据 | `cmd/link/internal/ld/decodesym.go` 的 `decodetypeGcprogShlibByReloc` |
| 测试标签 | `testing/benchmark.go` 的 goos 打印、`crash_cgo_test.go` 的 openharmony case |
| 链接器跳过 | `cmd/link/link_test.go`（race detector skip `|| runtime.IsOpenharmony`） |

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
```

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
