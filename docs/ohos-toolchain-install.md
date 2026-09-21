# OHOS Go 工具链安装（go1.27.1-ohos beta1）

## 这是什么

`golang/go` 的 fork，把 **OpenHarmony (OHOS)** 加成受支持的 `GOOS`。基线是上游
**go1.27.1**（`VERSION` 文件是权威）。发布形态是**预编译树 + tag**，所以你不需要
`./make.bash`（那要 3 阶段自举，还要一个 go1.24.6+ 的 bootstrap）。

本 beta 只发布 **darwin/arm64 宿主** —— 唯一实测过的宿主。支持的目标只有
`openharmony/amd64` 与 `openharmony/arm64`。

## 拿到与解包

```bash
shasum -a 256 -c go1.27.1-ohos-beta1-darwin-arm64.tar.gz.sha256
cd ~ && tar xzf /path/to/go1.27.1-ohos-beta1-darwin-arm64.tar.gz
# → ~/go1.27.1-ohos
```

**解出来的目录就是 `OHOS_GO_ROOT`。** 下游 `v2rayHM/scripts/build_libxray_ohos.sh`
默认取 `$HOME/go1.27.1-ohos`（`OHOS_GO_ROOT="${OHOS_GO_ROOT:-$HOME/go1.27.1-ohos}"`），
所以解到 `$HOME` 正好对上默认值，不用配任何变量。

**整棵树要原样保留。** `go` 靠**自身二进制所在路径**推 `GOROOT`，把 `bin/go` 单独拷走
就找不到 `pkg/tool`、`src`、`api` 了。要移动就整目录移动。

## 三条硬前提（少一条就报一个不相干的错）

### 1. `$OHOS_GO_ROOT/bin` 必须在 `PATH` 上

不是舒服不方便的问题，是**功能开关**：`go` 用 `pathcache.LookPath("go_<GOOS>_<GOARCH>_exec")`
（`cmd/go/internal/work/build.go:902`）找 `-exec` 包装，而它就是裸 `exec.LookPath`，
**没有 `$GOROOT/bin` 兜底**。不在 `PATH` 上的后果是交叉测试时试图在宿主上直接执行
aarch64 二进制，报 **`exec format error`** —— 一个完全指错方向的错。

```bash
export PATH="$HOME/go1.27.1-ohos/bin:$PATH"
```

### 2. `CC` 必须是 OHOS SDK 里 clang 的**绝对路径**

```bash
OHOS_SDK=/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony
export CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos \
  --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"
```

裸 `clang` 会解析到 Apple 的 `/usr/bin/clang`，它不认识 `aarch64-linux-ohos`，报
`clang: error: unable to execute command: posix_spawn failed: No such file or directory`。

**这个错很难自己冒出来**：只有需要**外部链接**的包才会调 clang，纯 Go 包一路内部链接。
所以配错 `CC` 的表现是「某一个奇怪的包坏了」而不是「工具链配错了」。

### 3. `GOTOOLCHAIN=local` 不要改

树里的 `go.env` 已经设好：

```
GOTOOLCHAIN=local
```

上游工具链**编不出** `GOOS=openharmony`。放开这一项，`go` 会在某个 `go.mod` 要求更高
版本时把自己**换成官方工具链**，然后你拿到的是一句难懂的构建失败。别动它。

## 交叉编译

```bash
export GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1
export PATH="$HOME/go1.27.1-ohos/bin:$PATH"
export OHOS_SDK=/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony
export CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos \
  --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"

go build -o app app.go                     # 可执行的 PIE
go build -buildmode=c-shared -o lib.so .   # 给 HarmonyOS 应用加载
```

- **默认就产出可运行的 PIE**（`platform.DefaultPIE` 对 openharmony 为 true），不用再加
  `-buildmode=pie`。
- **`-buildmode=c-archive` 必须 `AR=$OHOS_SDK/native/llvm/bin/llvm-ar`。** macOS 的
  `/usr/bin/ar`（cctools）拒收 ELF 成员，一个都不加**却返回 0** —— 静默产出 96 字节空库。
- 用 `c-shared` 时 **`-race` 不可用**、`-msan` 不可行，`-asan` 可用。理由见
  `docs/go-upgrade-guide.md` §4.2。

## 跑测试 / 在设备上执行

设备侧执行走 `-exec` 包装（`misc/go_openharmony_exec`，OHOS 版的 `go_android_exec`）。
它把宿主 GOROOT 镜像到设备、在设备上装一份原生 `go`，然后在那里跑。设备由两个变量选：

```bash
export OHOS_HDC=hdc                 # 默认就取 PATH 上的 hdc
export OHOS_TARGET=127.0.0.1:5555   # 默认就是模拟器
GOOS=openharmony GOARCH=arm64 go test strings
```

- **模拟器 `127.0.0.1:5555` 是唯一能 exec 的目标。** 商用真机的 shell 域不能 exec
  未签名二进制（`Permission denied`）。
- 设备侧工作目录是 `GOROOT/src/<importPath>` 的镜像，所以依赖 `testdata` 的用例能找到文件。
- **已知限制**：`hdc shell` 的回程**传不了 NUL**（NUL 处截断），且 shell 域（`uid=2000`）
  **不能 bind `127.0.0.1`**。后者是权限域限制，不是 port 缺陷 —— 应用域可以，
  证据见 `misc/openharmony/loopbackhap/README.md`。

## 改过工具链之后

**换工具链后必须清缓存。** 陈旧 `GOCACHE` 会让 reloc 枚举编号漂移，链接期报
`unknown reloc to ...: 105 (RelocType(105))`：

```bash
go clean -cache          # 或用新的 GOCACHE 目录
```

## 能力与已知问题

验收结论（每种 buildmode、sanitizer、TLS、x509 的实际可用性）在
`docs/go-upgrade-guide.md` **§4.2**，每条都附设备上的实测证据。
本 beta 的已知问题清单在 `docs/ohos-release-notes-v1.27.1-beta1.md`。

## 从源码重建（可选）

预编译树够用；要自己重建：

```bash
cd ~/projects/ohos-go/src
GOTOOLCHAIN=local GOPROXY=off GOROOT_BOOTSTRAP=/opt/homebrew/opt/go/libexec ./make.bash
```

需要一个 **go1.24.6+** 的官方工具链当 bootstrap（`GOROOT_BOOTSTRAP` 或 `PATH`）。
`-exec` 包装**不会被普通 `make.bash` 装上**（`cmd/dist/build.go` 的 `wrapperPathFor`
只在交叉自举时命中），要按 `docs/go-upgrade-guide.md` §4.2 末尾那两条命令手装。
