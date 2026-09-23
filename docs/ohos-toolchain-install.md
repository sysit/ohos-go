# OHOS Go 工具链安装（go1.27.1-ohos beta2）

## 这是什么

`golang/go` 的 fork，把 **OpenHarmony (OHOS)** 加成受支持的 `GOOS`。基线是上游
**go1.27.1**（`VERSION` 文件是权威）。发布形态是**预编译树 + tag**，所以你不需要
`./make.bash`（那要 3 阶段自举，还要一个 go1.24.6+ 的 bootstrap）。

本 beta 发布 **darwin/arm64** 与 **linux/amd64** 两个宿主 —— 两份都在各自宿主上全量
重建并实测过，不是交叉产出的。其他宿主可自行 `make.bash`（见文末）。
支持的目标只有 `openharmony/arm64` 与 `openharmony/amd64` —— **与宿主架构无关**。

> **版本串**：`VERSION` 与 `go version` 都报 **`go1.27.1-ohos`**，好区分 fork 与上游。
> （已发布的 **beta1** tarball 早于这处改动，里面报的还是裸 `go1.27.1`，与上游逐字相同。）

## 拿到与解包

挑你宿主的那份（`<宿主>` = `darwin-arm64` 或 `linux-amd64`）：

```bash
shasum -a 256 -c go1.27.1-ohos-beta2-<宿主>.tar.gz.sha256   # Linux 用 sha256sum -c
cd ~ && tar xzf /path/to/go1.27.1-ohos-beta2-<宿主>.tar.gz
# → ~/go1.27.1-ohos
```

**解出来的目录就是 `OHOS_GO_ROOT`。** 下游 `v2rayHM/scripts/build_libxray_ohos.sh`
默认取 `$HOME/go1.27.1-ohos`（`OHOS_GO_ROOT="${OHOS_GO_ROOT:-$HOME/go1.27.1-ohos}"`），
所以解到 `$HOME` 正好对上默认值，不用配任何变量。

**整棵树要原样保留。** `go` 靠**自身二进制所在路径**推 `GOROOT`，把 `bin/go` 单独拷走
就找不到 `pkg/tool`、`src`、`api` 了。要移动就整目录移动。

## 四条硬前提（少一条就报一个不相干的错）

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
Linux 宿主上没有这层同名冲突，但**仍然要写绝对路径** —— 系统装的 `clang` 一样不认这个 target。

`OHOS_SDK` 就是你自己那个 SDK 的根；上面是 macOS 上 DevEco Studio 的默认位置，
Linux 上通常是你解压的 commandline-tools 目录。

**`--target` 跟着目标架构走**：arm64 是 `aarch64-linux-ohos`，amd64 是 `x86_64-linux-ohos`。

**这个错很难自己冒出来**：只有需要**外部链接**的包才会调 clang，纯 Go 包一路内部链接。
所以配错 `CC` 的表现是「某一个奇怪的包坏了」而不是「工具链配错了」。

### 3. `GOTOOLCHAIN=local` 不要改

树里的 `go.env` 已经设好：

```
GOTOOLCHAIN=local
```

上游工具链**编不出** `GOOS=openharmony`。放开这一项，`go` 会在某个 `go.mod` 要求更高
版本时把自己**换成官方工具链**，然后你拿到的是一句难懂的构建失败。别动它。

### 4. 目标为 `amd64` 时，`CGO_ENABLED` **必须**是 `1`

```bash
export GOOS=openharmony GOARCH=amd64 CGO_ENABLED=1
```

**arm64 允许 `CGO_ENABLED=0`**（纯 Go 一路内部链接，连 SDK 都不需要），**amd64 不允许**。
amd64 的 TLS 走通用动态模型（`R_AMD64_TLS_GD`，因为 openharmony 默认 PIE ⇒ 编译器恒拿到
`-shared`），而**只有外部链接器实现了那个序列**，所以 amd64 的**每次**构建都强制外部链接。
漏掉这条的报错是

```
openharmony/amd64 requires external (cgo) linking, but cgo is not enabled
```

—— 指了方向，但没告诉你「为什么 arm64 行、amd64 不行」。机制与实测见
`docs/go-upgrade-guide.md` **§4.3**。（**beta1 的 tarball** 里这句报错还不存在：那棵树
amd64 的默认构建直接死在 `cannot handle R_AMD64_TLS_GD ... when linking internally`。）

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
  （这条是 **macOS 宿主特有**的坑；Linux 的 GNU `ar` 能吃 ELF，但统一写 `llvm-ar` 更省心。）
- 用 `c-shared` 时 **`-race` 不可用**、`-msan` 不可行，`-asan` 可用。理由见
  `docs/go-upgrade-guide.md` §4.2。

**换到 amd64 目标**（`CC` 的 `--target` 与 `CGO_ENABLED` 要跟 `GOARCH` 一起改，见硬前提 2、4）：

```bash
export GOOS=openharmony GOARCH=amd64 CGO_ENABLED=1
export CC="$OHOS_SDK/native/llvm/bin/clang --target=x86_64-linux-ohos \
  --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"
go build -o app-amd64 app.go
```

**amd64 的运行期没验过** —— 没有 amd64 设备（模拟器 `127.0.0.1:5555` 是 aarch64）。
构建期（编译 + 链接 + 共享库 GC 数据解码）已验，逐条见支持矩阵。

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
- ⚠️ **2026-09-23 之前下载的 beta2 包，这个包装是坏的**（两份宿主包都是）：它嵌着构建机的
  绝对路径，**解包到别处之后**跑设备测试时，凡是跨目录读 `testdata` 的用例都 ENOENT 假红
  （12 个包）。**不影响交叉编译。** 已修并**同名替换**了资产 —— **重新下一次**就行
  （tag 与提交没动，变的只有这个包装和几份文档），细节见 release notes 已知问题 7c。
- 拿到任何一棵树（自己重编的、或别人给的包）想验一下，跑这条 —— **每一行都必须是 0**：

  ```bash
  cd <树根> && for f in bin/*; do [ -f "$f" ] || continue
    printf '%6s  %s\n' "$(strings "$f" | grep -cE '^/[^ ]*\.go$')" "$f"; done
  ```

  判据是**「二进制里有没有绝对路径形态的 `.go` 串」**，与你在哪、树在哪无关 ——
  所以它能用来看**别人的包**。**别用 `grep -cF "$PWD"` 那种写法**：它只在
  「站在构建树自己的路径上」时有效，解到别处之后 `$PWD` 不是构建路径，计数**恒为 0**，
  缺陷再明显也照过（实测：首版 beta2 那份带 366 条绝对路径的包装，用那种写法查出来是 0）。

  万一需要自己重编包装（比如拿的是别处来的树）：`cd <树根>/misc && ../bin/go build -trimpath
  -o ../bin/go_openharmony_arm64_exec ./go_openharmony_exec && cp ../bin/go_openharmony_arm64_exec
  ../bin/go_openharmony_amd64_exec`（**`-trimpath` 不能省**）。


## 改过工具链之后

**换工具链后必须清缓存。** 陈旧 `GOCACHE` 会让 reloc 枚举编号漂移，链接期报
`unknown reloc to ...: 105 (RelocType(105))`：

```bash
go clean -cache          # 或用新的 GOCACHE 目录
```

## 能力与已知问题

验收结论（每种 buildmode、sanitizer、TLS、x509 的实际可用性）在
`docs/go-upgrade-guide.md` **§4.2**，每条都附设备上的实测证据。
本 beta 的已知问题清单在 `docs/ohos-release-notes-v1.27.1-beta2.md`。

## 上游跟进（这个移植怎么保鲜）

**承诺**：跟随上游 **minor** 版本（`go1.27.x` 的安全修复）；**major**（`go1.28` 及以后）
合并视验证情况，**不承诺时间窗**。

措辞是刻意的：major 会动到本移植的承重处（TLS 重定位编号、musl emulation、平台表），
合并必须重跑三层验证，给时间窗等于开空头支票。minor 只带修复，风险低，所以敢承诺。

合并剧本在树里的 `docs/go-upgrade-guide.md` —— 要点是**先抽 delta 再合、合完拿残留
diff 与新上游 tag 逐文件对账**；**§4.1 那张「必须保留」的表就是验收清单**。
每代都要盯的三处：`asm7.go` 的 optab case 号（arm64 TLS_GD 用的号会被上游占走）、
`R_AMD64_TLS_GD` 的枚举值、`MustLinkExternal` 的两处副本。

**判断拿到的是哪棵树的工具链**：`go version` 带 `-ohos` 后缀。不带的就是上游的（或 beta1
的 tarball，它早于这处标记）。

## 从源码重建（可选）

预编译树够用；要自己重建：

```bash
cd ~/projects/ohos-go/src
GOTOOLCHAIN=local GOPROXY=off GOROOT_BOOTSTRAP=/opt/homebrew/opt/go/libexec ./make.bash
```

需要一个 **go1.24.6+** 的官方工具链当 bootstrap（`GOROOT_BOOTSTRAP` 或 `PATH`）。
**两个宿主都已实测**：

- **darwin/arm64**：bootstrap 用 homebrew 的 `go`（`/opt/homebrew/opt/go/libexec`）。
- **linux/amd64**（Debian 13，8 核）：bootstrap 用 `golang-1.25-go`，路径
  `/usr/lib/go-1.25`。注意 Debian 的 `golang-1.24-go` 是 **1.24.4**，差一点点没够 1.24.6
  的门槛 —— 装 1.25 那包。

重建完要**手装 `-exec` 包装**：普通 `make.bash` **不会**装上它（`cmd/dist/build.go` 的
`wrapperPathFor` 只在交叉自举时命中，且**它缺席时不会有任何提示**）。命令见
`docs/go-upgrade-guide.md` §4.2 末尾。
