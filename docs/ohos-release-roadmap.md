# OHOS Go 工具链：beta → release 还差什么

> 2026-09-22 起。`v1.27.1-beta1` 已发布（tag + darwin/arm64 tarball）。
> 本文件记录 **beta 与 release 之间的距离**，按「会不会咬到你」排序，不按难度。
>
> **核心判断**：鸿沟**不在构建质量** —— 三层独立验证、每条 FAIL 都有证据串、
> 连「全绿≠没缺陷」的反向教训都写进文档了，比多数 fork 细。
> 鸿沟在：**beta = 「我们验过这个快照」（一次性动作）**
> vs **release = 「别人可以依赖这个平台」（持续状态）**。
> 测试类的活儿基本做完了，缺的全是让它*活着*的那部分。

## 执行顺序

~~① x509 应用域复验~~ ✅ → ~~② A 层重跑~~ ✅（顺带修掉 10 个丢失的 testdata）→ ~~③ 版本串~~ ✅ → ~~④ linux/amd64 宿主~~ ✅（**挖出一个真缺陷，已修**）→ ~~⑤ amd64 目标定性~~ ✅（原论断被推翻；补了一条真缺陷）→ ~~⑥ CI~~ ✅ → ~~⑦ 跟进承诺~~ ✅ → ~~⑧ 产物对账~~ ✅（又挖出一条 delta，见下）→ **⑨ 下次 cut 的待办**

**①~⑧ 已闭环。** 前四条**一条 port 源码都不用改**（② 的两条真缺陷是**丢失的 testdata**）；
**④ 挖出的那条要改代码，是本轮唯一一处 port 源码改动**：`openharmony/amd64` 的
**默认构建此前 100% 编不出来**，已照上游 `android/amd64` 的先例修掉（详见 ④ 与 ⑤ 补）。
**⑧ 是收尾后拿产物对账才发现的** —— 同一类盲区第二次咬人，见那节的教训。

---

## ① x509 应用域复验 —— ✅ 已闭环（2026-09-22 晚）

**结论：这条不是缺陷。** release notes 原先记的「x509 取不到系统根，让下游用
`SSL_CERT_FILE`」是把 **`sh` 域的测试载体限制当成了平台限制** —— 与环回那 17 个包
**同一个错误形状**（同路径、两域、结论相反）。

HAP 夹具（`misc/openharmony/loopbackhap/`，进程 `uid=20020077` / `u:r:app:s0`）应用域实测：

| Go 实际做的（`crypto/x509/root.go:183-197`） | 应用域结果 | 探针 |
|---|---|---|
| `os.ReadDir("/etc/ssl/certs")` | OK，1 条目 `cacert.pem` | B |
| 该条目**不是**同目录软链 | `lstat → symlink=false` | SC |
| `os.ReadFile("/etc/ssl/certs/cacert.pem")` | OK，191450 B，真 PEM | C |
| `AppendCertsFromPEM`（同一份字节在宿主上跑） | **125/125 解析成功** | — |

⇒ `roots.len() > 0`，`root.go:199` 返回池子。上游 6 个 `certFiles` 在 OHOS 全 ENOENT（被忽略），
但 `certDirectories[0] = "/etc/ssl/certs"` **正好命中 OHOS 的 bundle 目录**。
**⇒「零 delta」这个结论是对的，但理由与当初写的相反。**

第 2 行是这条链子上的隐藏杀手：`readUniqueDirectoryEntries`（`root.go:206`）会跳过
**目标串不含 `/` 的 symlink**。该目录下只有 `cacert.pem` 一个条目 —— 若它是同目录软链，
Go 会把它剔掉，得到**空池 + nil error**，即三态里最坏的那格（静默，等 TLS 握手才报
unknown authority）。前三个探针看不出这个，必须单独 `lstat`。

**剩下的真缺口只有一条，已登记不修**：`/data/certificates/user_cacerts`（OHOS 用户 CA 位置）
应用域 `open` 得 EACCES（`lstat` 说它存在、非软链）→ **用户自装 CA 取不到**。
上游给 Android 加的那两行照抄无效，因为问题不是「路径不对」而是「权限不给应用」；
正解是 OHOS cert framework（`@ohos.security.cert`）+ cgo，是**真特性不是补丁**。本 beta 接受。

**没做的（诚实交代）**：没在应用域里跑**真的 Go 二进制**调 `SystemCertPool()`。要做就得建
native 载体（HAP + Go c-shared `.so` + N-API 胶水）—— 那是另一摊工作量。现在的证据是
**逐 syscall 对齐**的：Go 要读的路径、顺序、每次系统调用，都在应用域用同一批 musl syscall
（`open`/`readdir`/`read`）验过，第 4 环用**同一份字节**在宿主上验的。剩下的推断只有
「ArkTS 的 `open` ≡ Go 的 `open`」。**判断：这条推断不值得为它建一个 native 载体。**
将来若要端到端铁证，载体就是这个形态。

已回写：`ohos-release-notes-v1.27.1-beta1.md`（能力表两行 + 已知问题 2 重写 + 与上一代差异）、
`ohos-full-test-plan.md`（§5.3 收口段 + 四处引用）、`loopbackhap/README.md`。

---

## ② A 层 `./all.bash` 重跑 ✅ 已跑完（2026-09-22）

**结果：3 个包 FAIL，其余全绿。三条全部定性 —— 2 条是真缺陷（已修），1 条是环境。**

| 包 | 症状 | 定性 |
|---|---|---|
| `cmd/go/internal/modfetch/codehost` | 5 个用例 `fatal: ... Empty reply from server` | **环境，非 port 缺陷** |
| `cmd/objdump` | `TestGoObjOtherVersion`: `open testdata/go116.o: no such file or directory` | **真缺陷，已修** |
| `go/internal/gccgoimporter` | `TestGoxImporter`: `could not find export data (tried testdata)` | **真缺陷，已修** |

- **codehost**：`~/.gitconfig` 里的 `http.proxy socks5://172.16.1.254:1080` 把测试自己起的本地
  `127.0.0.1:58596` 也塞进了 socks 代理。`GIT_CONFIG_GLOBAL=/dev/null` 与 `no_proxy=127.0.0.1`
  两种改法都实测转绿（后者用 `-count=1` 强制，排除测试缓存）。**与 OHOS 无关。**
- **后两条同一根因**：**10 个上游跟踪的二进制 testdata 被 `.gitignore` 吞掉**（fork 导入时丢的）。
  已按上游 blob sha **逐字节校验**补回并 `git add -f`（**未提交**）。复现 + 泛化检查
  （`comm` 比对上游客 vs 本地 tracked，结果 **0 行缺失**）写进 `docs/go-upgrade-guide.md` **§3.5**。
  顺带纠正了 release notes 已知问题 7 的误诊（原写「上游就没有，要往上游提」）。
- **一个反向收获**：`go/internal/gcimporter` 原本报 `ok`，其实是因为那 8 个 `.a` 缺席而**静默跳过**版本用例
  ——补回后同一包 6.3s → 27.7s，**才真的在跑**。「全绿」和「全红」一样会藏东西。

**A 层结论：基线没坏。** 唯一的红是使用者自己的 socks 代理配置。

```bash
cd src && GOROOT_BOOTSTRAP=/opt/homebrew/opt/go/libexec ./all.bash
```

### ② 补（2026-09-22，用**最终树**再跑一遍）

上面那轮跑在修 testdata **之后**、改 ④⑤ 那处代码**之前**。改完代码又在最终树上跑了一遍：

**结果：1 个包 FAIL —— 只剩那条环境干扰；两条真缺陷确实修掉了。**

```
FAIL	cmd/go/internal/modfetch/codehost	3.189s     ← 就是 ~/.gitconfig 的 http(s).proxy
```

**这次补了一个可复现的判据**（上一轮只靠推断）：同一棵树、同一个包，只把全局 git 配置置空 ——

```bash
GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
  ../bin/go test -count=1 ./cmd/go/internal/modfetch/codehost
#   → ok  	cmd/go/internal/modfetch/codehost	5.423s
```

**转绿**。所以这条是使用者的 `http.proxy`/`https.proxy` 把环回也代走了，与 port 无关；
**与本次代码改动也无关** —— 该包只是 `exec` git，根本不碰链接模式。

**改动本身的定向回归**（比全量更有针对性）：`cmd/dist`、`internal/platform`、
`cmd/link/internal/ld`、`cmd/go/internal/load` 四个受影响包全 `ok`
（含 `TestMustLinkExternal` 对两处副本的逐格比对）。

---

## ③ 版本串不可区分 fork ✅ 已落码并验收（2026-09-22）

`VERSION` 第 1 行 `go1.27.1` → `go1.27.1-ohos`，已 `./make.bash` 整棵重建。五条验收全过：

| 验收 | 结果 |
|---|---|
| `bin/go version` | `go version go1.27.1-ohos darwin/arm64` |
| **release 标志没丢**（§6.8 判据） | `strings bin/go \| grep -c '<树绝对路径>'` = **0** ⇒ `-trimpath` 生效 |
| `-V=full` | `compile version go1.27.1-ohos`，**无 ` buildID=`**（不含 `devel`，与上游 release 同行为） |
| `go/version` + `gover` | 两包 `ok` |
| `cmd/api`（`-run TestCheck`） | `ok` ⇒ **仍是 release 语义**，没被翻成开发版 |
| `cmd/go -run Script/version*` | `ok` |
| **端到端** | 宿主与 `GOOS=openharmony GOARCH=arm64` 产物 `go version -m` 均报 `go1.27.1-ohos`；产物仍是 PIE + `/lib/ld-musl-aarch64.so.1`，无回归 |

**收益**：下游把 `go version -m` 的输出贴进上游 issue，上游一眼能看出不是官方工具链；
反向也只需一行版本号就能判断用户装的是哪棵树。

**代价**：`hashSalt`（`cmd/go/internal/cache/hash.go:46`）就是版本串 → 升级后首次构建全量重编一次
（设计意图，不是缺陷，release notes 需提一句）；新旧工具二进制混用会响亮报错
`compile: version … does not match go tool version …`，改后必须整棵重建。

**时序**：本改动**晚于 `v1.27.1-beta1`**，随下一次 cut（beta2 / final）发布。
beta1 的 tarball 里仍是 `go1.27.1`，那是当时的事实，不改写。

---

## ③-原始侦查记录（改动点是怎么定的）

`VERSION` 是裸 `go1.27.1`，实测 `./bin/go version` → `go version go1.27.1 darwin/arm64`，
**逐字与上游相同**。

后果：下游把这段贴进上游 issue，上游会在官方 toolchain 上找一个不存在的问题；
反过来我们也无法从一行版本号判断用户装的是哪棵树。

### 结论：改 `VERSION` 第 1 行 = `go1.27.1-ohos`。**不需要**动 `runtime.buildVersion`。

原记录里「给 VERSION 加后缀可能连带丢掉 release 标志，所以多半要落在
`runtime.buildVersion`」这句**推错了**，理由见下；落点就在 `VERSION`，一行。

链路与逐环验证（2026-09-22 实测/读码）：

| 环 | 事实 |
|---|---|
| `findgoversion()`（`cmd/dist/build.go`） | **第 1 行原样返回**，不校验格式；只校验第 2 行起，且只允许 `time <RFC3339>` |
| `isRelease()`（`build.go:279`） | `前綴 release.\|go` **且不含 `devel`** —— `-ohos` 两个条件都过 → 仍是 release，`-trimpath -ldflags=-w` 门照旧触发 |
| `go/version` | 实测 `go1.27.1-ohos` → `IsValid=true`、`Lang=go1.27`、`Compare(…,"go1.27.1")==0` |
| `gover.FromToolchain` | 同上 → `1.27.1`，`go` directive 比较不受影响 |
| `cmd/api/main_test.go:110` | 含 `beta`/`devel` 才挂 `api/next/*.txt`（开发版语义）。`-ohos` 不含 → **保持 release 语义** |
| `cache/hash.go:46` `hashSalt` | = `stripExperiment(runtime.Version())` → 盐变了，**整棵构建缓存换代一次**（期望行为：不该与上游共用缓存条目） |
| `work/gc.go:112` `-goversion` | 传给 compiler 的值与 `compile` 自身的 `runtime.Version()` 同源 → 自洽；但**新旧二进制混用会响亮报错**（`compile: version … does not match go tool version …`），改了必须整棵重建 |
| `objabi/flag.go:232` `-V=full` | 只有含 `devel` 才附 `buildID=` —— 与上游 release 同行为，非 delta |
| `cmd/internal/bootstrap_test/reboot_test.go:65` | 写 `[]byte(runtime.Version())` 当 VERSION 再重建。`go1.27.1-ohos` 是不动点 → 收敛 |
| `version_goexperiment.txt` | 只断言 `X:fieldtrack$` 与 `IsValid` → 安全 |
| `build_version_stamping_git.txt` | 只断言 `mod` 行 → 安全 |

**后缀选择是有雷区的，别写错**（三条都实测过）：

- `go1.27.1+ohos` → `go/version` **无效**（`IsValid=false`）→ `version_goexperiment.txt` 直接 panic
- `go1.27.1ohos` → 同样无效
- `go1.27.1-beta1` / 任何含 `beta` → `cmd/api` 翻成开发版语义；含 `devel` → `isRelease` 变假、`findgoversion` 转去走 git tag 路径、`-V=full` 开始附 buildID

### 验证（改完后，最小集）

```bash
cd src && ./make.bash                    # 必整棵重建，见上表 `-goversion`
../bin/go version                        # 期望 go version go1.27.1-ohos darwin/arm64
../bin/go test ./go/version
../bin/go test -short ./cmd/go/internal/gover
../bin/go test -short ./cmd/api          # 必须是 release 语义
../bin/go test -short ./cmd/go -run=Script/version   # 两条 version*.txt
```

### 代价 / 时序

- **会让构建缓存换代一次**（每个用户升级后首次构建全量重编）。这是 `hashSalt` 的设计意图，不是缺陷，但要在 release notes 提一句。
- **会让已发布的 `v1.27.1-beta1` tarball 与 `go version` 输出不符** ——
  所以这项**不动 beta1，随下一次 cut（beta2 / final）一起发**。现在改会让 beta1 的 tarball 变成孤儿。

---

## ④ 宿主只有 darwin/arm64 ✅ 已闭环（2026-09-22，在 `172.16.1.238` 上实测）

支持矩阵写着「darwin/arm64 唯一（本 beta 只实测过这一种）」。
Linux 用户（OHOS 开发主力）拿到 tarball 用不了，得自己 `./make.bash` ——
而 **`make.bash` 在 Linux 上能不能过没人验过**。

机器：`172.16.1.238`（Debian 13 trixie，8 核，tuna apt 源）。装的是
`gcc libc6-dev golang-1.25-go` —— 引导必须 **Go 1.25.12**，因为 Debian 的
`golang-1.24-go` 是 1.24.4，**低于 `make.bash` 要求的 1.24.6**（差一点点，正好卡在门槛下）。

| 检查 | 结果 |
|---|---|
| `GOROOT_BOOTSTRAP=/usr/lib/go-1.25 ./make.bash` | **EXIT=0**，`Installed Go for linux/amd64`，`go version go1.27.1-ohos linux/amd64` |
| 宿主自测 `go test strings strconv sort` | 全 `ok` |
| 同一份源码在 Linux 宿主上原生构建 + 运行 | OK |
| Linux 宿主 → `GOOS=openharmony GOARCH=arm64 CGO_ENABLED=0 go build` | **OK**（纯 Go 不需要任何 SDK） |
| Linux 宿主 → `GOOS=openharmony GOARCH=amd64 CGO_ENABLED=0 go build` | 改前 **链接器 Fatalf**；改后一句指方向的 cgo 报错（见下） |

⇒ **Linux 作为宿主可行，「Linux 用户能用」这半边成立**。但 ④ 顺带划出一条**新的边界**：
**cgo 产物与整个 amd64 目标都要 OHOS SDK**（`172.16.1.238` 上没装，这两条只在 macOS + 真 SDK 上验过）。
不需要 SDK 的组合只有一种：**纯 Go + arm64** —— 这条在 Linux 上实测通。

**而且这一步挖出一个真缺陷**（下面「⑤ 补」）：`openharmony/amd64` 的默认构建**此前必然失败**。
不是 Linux 独有，macOS 上一样 —— 只是**此前从没人试过 amd64 的非 cgo 构建**（试过的都是
c-shared / `-buildmode=shared`，两者都强制外部链接，恰好绕过）。

**tarball 的含义随之明确**：预编译 tarball 是 **darwin/arm64** 的（`$GOROOT/bin/go` 是宿主二进制）。
Linux 用户拿它**用不了** —— 要么等 Linux tarball，要么自己 `./make.bash`（现已验可行，
引导只要 ≥1.24.6）。这是发布说明里必须写明的一句。

---

## ⑤ amd64 目标定性 ✅ 已闭环（2026-09-22）—— 原论断被推翻

**这条是误读，不是缺陷。** 原记录写「amd64 的 shlib 链接拿不到 map 时**静默回落到零 gcprog**
—— GC 数据错，不报错。这比「未验」更糟」，并给了「找设备实测 / 降级为 experimental」二选一。

**误读怎么发生的**：`docs/ohos-full-test-plan.md` §5.5 那张表是**三列**（缺陷 / 症状 / 修法）。
「只处理 `EM_AARCH64`」「静默回落到零 gcprog」都在**左两列**，描述的是**修复前**的状态；
右列的「**仅 arm64 实测**（无 amd64 设备）」说的是**验证覆盖**，不是**缺陷仍在**。
把左列的旧症状读成现状，就得到了一个不存在的开放缺陷。**同一个错误形状值得记下来：
读三列表时，先确认自己读的是哪一列。**

实测与代码复核（2026-09-22，**纯宿主侧，不需要 amd64 设备**）：

| 判据 | 结果 |
|---|---|
| amd64 是否在 map 覆盖范围内 | **在**。`getRelocAddendMapShlib`（`lib.go:2926`）对 `EM_X86_64` 走 `R_X86_64_RELATIVE` 分支 → 架构中立的 `getRelocAddendMapShlibELF64` → 返回 `make(map…)`，**非 nil** |
| amd64 shlib 的形状 | 与 arm64 **逐字段相同**：`.rela.dyn` 是 RELA（entsize `0x18` = 代码里的 `entSize := 24`）；76831（arm64 是 76774）条 `R_X86_64_RELATIVE`；`r_info = 0x8` ⇒ **符号索引 0**；addend 非零；盘上字段抽样 8/8 **全为 0**（值只在 `.rela.dyn`）|
| 静默分支是否可达 | **不可达**。返回 `ok=false` 的静默路只有 `addendMap == nil`（`decodesym.go:244`），它只对**既非 arm64 也非 amd64**的机器成立 |
| 危险情形是静默还是响亮 | **响亮**。map 非 nil 却没查到 addend 且 `ptrdata != 0 && gcprog == 0` → `Exitf`（`decodesym.go:258-263`）；能安静回退的只剩 `ptrdata == 0`（零 gcprog 本就正确）|
| 端到端实跑 | 临时插桩 linker（`-toolexec` 换 link，验完**已还原**），`-linkshared` 链一个「数据段全局变量 + 类型定义在 shlib 里 + default kind」的探针：**amd64 5/5 命中，arm64 5/5 命中**，`miss`/`nosymvalue`/`nilsmap` 全 0。全新 `GOCACHE` 复跑一致 |

⇒ lib.go:2939-2943 那条 `ponytail:` 注释里的「amd64/asm.go emits the same r_info with a
zero symbol index」**由推测变成实测**。证据已回写 `docs/ohos-full-test-plan.md` §5.5。

**剩下的真限制只有一条，且是诚实性问题不是缺陷**：amd64 **没有任何运行期验证**
（无 amd64 设备；模拟器 `127.0.0.1:5555` 是 aarch64）。准确措辞是

> `openharmony/amd64` = **构建期已验**（编译 + 链接 + shlib gcprog 解码 —— 宿主侧可验的全部），
> **运行期未验**（无设备）。

**倾向（已按此改写文档）**：**保留 amd64**，把支持矩阵与 release notes 的措辞换成上面那句，
并把「构建期 / 运行期」这个区分**同样套到 arm64 上**（arm64 运行期验过、amd64 没验过 ——
这才是两者真正的差别）。**不降级** —— 基于一个已被推翻的论断删平台，比写清事实更糟。

将来若拿到 amd64 OHOS 设备，`misc/openharmony/runtests.sh` 阶段 2 原样可跑。

### ⑤ 补（2026-09-22）：挖出的真缺陷 —— amd64 默认构建必然链接失败（**已修**）

跑 ④ 时顺手试了一条此前没人试过的组合，它挂了：

```
link: cannot handle R_AMD64_TLS_GD (sym net.SplitHostPort) when linking internally
```

**机理**（两处各自都合理，叠起来就是死）：

1. `cmd/internal/obj/x86/asm6.go` 的发射条件是
   `ctxt.Tls == "GD" || (isOpenharmony && ctxt.Flag_shared)`。openharmony 的 `DefaultPIE` 为真
   ⇒ 默认构建就是 PIE ⇒ **cmd/compile 拿到 `-shared`**（实测：一轮默认构建里 **465 次 compile
   调用全带 `-shared`**）⇒ `Flag_shared` 恒真 ⇒ **每次默认构建都为 g 寄存器重载发 `R_AMD64_TLS_GD`**。
2. 内部链接器**没有**这条重定位的实现（`ld/data.go:353` 直接 `log.Fatalf`），只有外部路径有
   （`amd64/asm.go:475`）。而 `MustLinkExternal` 里 openharmony **只在 `withCgo` 分支返回 true**
   ⇒ 非 cgo 构建走内部链接 ⇒ 必死。

**为什么 arm64 没事**：`asm7.go` 只在汇编显式 `MOVW $tlsvar`（case 101）时发 `R_ARM64_TLS_GD`，
编译器产生的代码不走那条。**为什么现在才发现**：amd64 此前只被跑过**强制外部链接**的路径
（c-shared、`-buildmode=shared`），那些都通。

**为什么不在编译器侧收窄**：PIE 与 c-shared 拿到的都是 `-shared`，**编译器分不出来**
（要分就得改 cmd/go 的 flag 面，比这个修法大得多）。

**修法**（照上游 `android/amd64` 的既有先例，3 行）：
`if goarch != "arm64" { return true }`，`internal/platform/supported.go` 与
`cmd/dist/build.go` 的引导期副本**两处都要改**（`TestMustLinkExternal` 逐格比对两者，漏一处就红）。

**代价（不是免费的，必须写进文档）**：amd64 的**每次**构建（含纯 Go）现在都要求
`CGO_ENABLED=1` + SDK clang，否则报
`openharmony/amd64 requires external (cgo) linking, but cgo is not enabled`。
这与**上游对 android/amd64 的取舍逐字同形**；且 amd64 本来零运行期证据，
换来的是从「必然 Fatalf」变成「能编出产物」。**arm64 完全不受影响**（仍走内部链接）。

**验证 5/5**：

| 用例 | 改前 | 改后 |
|---|---|---|
| amd64 纯 Go（默认 PIE） | 链接器 Fatalf | 一句 `requires external (cgo) linking` 的**指方向**报错 |
| amd64 + `CGO_ENABLED=1` + SDK clang（`--target=x86_64-linux-ohos`） | — | **通过**，7909128 字节 |
| amd64 `-buildmode=pie` | 链接器 Fatalf | **通过**，7909128 字节（与默认同形） |
| amd64 `-buildmode=exe` | 通过（内部链接，非 PIE） | **通过**，7595480 字节（现在改走外部） |
| amd64 `-buildmode=c-shared` | 通过 | 通过 |
| arm64 纯 Go / `-buildmode=pie` / `-buildmode=exe` | 通过 | **全部通过**（仍内部链接，无回归） |
| arm64 `-buildmode=c-shared`（主路径回归） | 通过 | 通过 |
| `cmd/dist` 的 `TestMustLinkExternal`（两副本逐格比对） | 通过 | 通过 |
| 受影响包宿主测试 `cmd/dist`、`internal/platform`、`cmd/link/internal/ld`、`cmd/go/internal/load` | 通过 | 全 `ok` |
| **linux/amd64 宿主**上的同一矩阵（`172.16.1.238`） | — | 与 macOS **行为一致**（新代码在那边也生效） |

产物形状（宿主侧能拿到的最强证据）：`ELF64 / DYN(PIE) / X86-64`、
`PT_INTERP = /lib/ld-musl-x86_64.so.1`（架构正确的 musl 解释器）、`PT_TLS` 在位、
动态重定位 **15922 `R_X86_64_RELATIVE` + 0 条 TLSDESC**、反汇编里 GD 惯用法 `call *(%rax)`
**0 次**、`%fs:` 段访问 **778 次** ⇒ **lld 把 TLSDESC 松弛成了 LE**，与 arm64 PIE 已在模拟器上
跑通的模型一致；而 arm64 的 **c-shared 库保留 1 条 `R_AARCH64_TLSDESC`**（被 dlopen 的库必须
走描述符）。**GD 出现在需要它的地方、LE 出现在安全的地方，由链接器决定** —— 这正是外部链接
不可替代的理由，也是这个修法比「自己给内部链接器补 GD」更该选的原因。

**这条修得动 ⑤ 的结论吗**：不动。amd64 仍是「构建期已验、运行期未验」，**仍然保留**；
只是「构建期已验」这句话从今天起才真正成立（此前默认构建根本编不出来）。

**B / C 两层的设备证据失效了吗**：没有，**不需要重跑设备侧**。理由是这次改动的返回值为
`goarch != "arm64"`，**在 arm64 上恒为旧值** —— 代码路径逐字未变（上面 7 条 arm64 回归用例
也逐条实测过）。B 层 262 个有测试的包与 C 层 2739 例跑的正是 arm64 目标，结论继续有效。
**A 层（宿主）已用最终树重跑**，见下。

---

## ⑥ 零 CI ✅ 已补（2026-09-22）

新增 `.github/workflows/ohos.yml`。此前这个 port 的每一次验证都是手工一次性、结论记在
散文里 —— **下一个 commit 可以静默弄坏 arm64 而没人知道**。

**先认下的限制**：设备侧要模拟器 + `hdc`，放 GitHub runner 上不现实。所以 CI **只覆盖
宿主侧**，设备侧仍靠 `misc/openharmony/runtests.sh` 手工跑。**它保证的是「宿主编译器
没坏」，不是「产物能跑」** —— 这两层本来就不互相背书。

| job | 触发 | 干什么 |
|---|---|---|
| `host` | 每 push / PR，`ubuntu-24.04` + `macos-14` 双宿主 | `宿主身份` → `make.bash` → 版本串断言带 `-ohos` → 9 个承重包的宿主测试 → misc 包装单元测试 → 交叉编译冒烟 → 打包自检 |
| `all-bash` | 仅夜间 schedule / 手动 | 全量 `./all.bash`。太重，不参与 PR 门禁，但它是唯一能证明「基线没坏」的一层 |

**冒烟是双向断言**，这是它的要点：`arm64` 纯 Go **必须成功**（内部链接，不需要 SDK）；
`amd64` 纯 Go **必须失败**，且必须是 `requires external (cgo) linking` 那句 ——
它若**成功**，说明 ⑤ 修的那条 delta 丢了。9 个包里 `cmd/dist` 是关键那个：
它的 `TestMustLinkExternal` 逐格比对 `MustLinkExternal` 的两处副本。

**刻意不做 GOCACHE 缓存**：陈旧缓存会让 reloc 枚举编号漂移，链接期报
`unknown reloc to ...: 105 (RelocType(105))`（本项目反复踩过）。每次从零编译换确定性。

**每条命令都先在本地按原样跑过**（darwin/arm64 + 仓库 `bin/go`，含 `GOPROXY=off`
的 CI 同款 env），不是写完就推。

**首跑结果**：`宿主侧 (ubuntu-latest)` 4m7s ✅、`宿主侧 (macos-14)` 5m24s ✅、
`all-bash` 按设计跳过。**两个宿主都绿** —— 顺带把「Linux 宿主的 CI 等价物」也证了。

**linux/amd64 的 A 层基线：全绿。** 在 `172.16.1.238` 上用**空的 GOCACHE** 跑了完整
`./all.bash`（8 核约 25 min）：**377 包 ok / 0 FAIL**。这条是夜间 job 的期望值 ——
**先量出基线再设期望**，否则一上线就是个红的 job。

> 量基线时踩了一个**同步假红**，值得记：第一次跑出 `cmd/api` 的 `TestGolden` 失败
> （`open testdata/src/pkg: no such file or directory`）。原因是我 rsync 时写了
> `--exclude 'pkg/'` —— 这模式**匹配任意深度**的 `pkg/` 目录，把上游签入的
> `src/cmd/api/testdata/src/pkg` 与 `src/simd/testdata/pkg` 一起排掉了。
>
> **树本身没问题**：`.gitignore` 用的是锚定的 `/pkg/`，这两个目录正常受版本控制，
> 上游存在性对账 0 缺失（见 `go-upgrade-guide.md` §3.5）。加上锚定斜杠后 `cmd/api` 转 `ok`。
> **教训：假红不一定来自环境，也可能来自你自己的搬运方式** —— 与 ①② 那两条
> 「`sh` 域观测不可信」是同一类错误的另一个面。

**⚠️ CI 环境漂移预警（2026-09-23 记，尚未发生）。** `ubuntu-latest` 从 **2026-10-19**
起迁到 Ubuntu 26。上面那条基线（377 ok / 0 FAIL）是在 24.04 上量的，漂移后
**红的可能是宿主而不是代码**，且没有任何东西提醒你宿主换了。已处置两块：

- 矩阵里 ubuntu 一格**写死 `ubuntu-24.04`**（与 `macos-14` 对称），把这次切换
  从「悄悄发生」变成「改一行 + 重量基线」的显式动作；
- 加了一步「宿主身份」，把 `os-release` / `uname` / `cc` / bootstrap 版本打进日志 ——
  固定标签的镜像**本身也会按月更新**，这步是唯一能事后回答「当时是哪台机器」的东西。

**2026-10-19 之后要做的**：显式升到 `ubuntu-26.04`，在新宿主上量一次 A 层基线，
再改期望值。在那之前「双宿主绿」指的就是 24.04 这一格。

---

## ⑦ 上游跟进节奏 ✅ 已定（2026-09-22，用户拍板）

**三条决定**：

1. **节奏**：跟上游 **minor**（go1.27.x 的安全修复 —— 下游全是做 TLS 代理的，对
   CVE 敏感）**+ major**（go1.28）。
2. **谁执行**：Claude 执行、用户审。上游 tag 出现后按 `docs/go-upgrade-guide.md` +
   `.claude/skills/merge-upstream/` 合并 → 跑三层验证 → 交一份「残留 diff 对账 +
   三层结果」的结论，**用户点头才推**。用户的持续投入接近零，只在合不动时被叫。
3. **承诺措辞**：**跟随上游 minor；major 合并视验证情况，不承诺时间窗。**
   写进 release notes 与 `docs/ohos-toolchain-install.md`（后者的「上游跟进」一节
   是给下游看的落点）。

**为什么这条值钱**：合并剧本是本项目最好的可复用资产，但「谁记得去看上游有没有
发新版」原来是空的。beta 可以是一次性快照；release 意味着有人跟着 go1.28、go1.29
走，否则半年后它是一棵没人敢升的死树。

**触发那一半也定了（2026-09-23，用户拍板）：新版本由用户触发。**

原备选是加一条定时 workflow（`git ls-remote` 比 tag，发现新的就开 issue），**不做** ——
开 issue 是对外的副作用，而用户本来就会看到上游发版。所以这里不引入任何自动轮询：
**上游 tag 出现 → 用户喊一声 → 按上面的流程合并。** 代价是「没人喊就没人合」，
但这是用户明确选的，不是遗漏。

---

## ⑧ `go.env` 的 `GOTOOLCHAIN` 曾丢掉 ✅ 已修（2026-09-23，收尾后对账才发现）

拿仓库树与已装树 `~/go1.27.1-ohos` 逐文件对账，根 `go.env` 两边不一致：

- **仓库**（= 下次打包的源）：`GOTOOLCHAIN=auto` —— **上游的原值，OHOS 那行不在**
- **已装树**（= 已发出去的 beta1）：`GOTOOLCHAIN=local` + 一段解释注释

`git log -- go.env` 显示本分支只有一个提交动过它（`4cc31134303`，7 月），而且是**原样搬上游**。
也就是说 **这条 delta 从来没进过仓库** —— 已装树里的 `local` 是某次手工改的，**不可从仓库复现**。

机制，实测（`/tmp/tcprobe`，一个 `go 1.28` 的模块）：

```
GOTOOLCHAIN=local → go: go.mod requires go >= 1.28 (running go 1.27.1; GOTOOLCHAIN=local)
GOTOOLCHAIN=auto  → go: downloading go1.28.0 (darwin/arm64)
```

`auto` 就是**把本移植换成官方工具链**，而官方工具链编不出 `GOOS=openharmony` ——
正是 `docs/ohos-toolchain-install.md` 硬前提 §3 警告的那件事。

**为什么 CI 没抓到**：workflow 顶上的 `env: GOTOOLCHAIN: local` 把运行期钉死了，
所以这条 delta 丢了 CI 照样绿。已补一条**直接查文件**的断言
（`grep -qx 'GOTOOLCHAIN=local' go.env`），并加进 `go-upgrade-guide.md` §4.1 那张表。

**普适教训**：这条 delta 的症状**只出现在下游**（用户的模块里），本仓库怎么跑都看不见 ——
与「上游有、本地缺的文件 `git diff` 看不见」是同一类盲区。**对账要看产物，不能只看自家测试绿不绿。**

---

## ⑨ 下一次 cut 的待办 ✅ 已闭环（2026-09-23，随 beta2 发出）

1. **同时出 linux/amd64 tarball**（用户拍板）。宿主可行性已在 ④ 验过，打包配方见
   `docs/go-upgrade-guide.md` §6.8。
   → **已出**：`go1.27.1-ohos-beta2-linux-amd64.tar.gz`（约 72 MiB；字节级凭据看同名
   `.sha256`），在 238 上**本机全量重建**（不是交叉产出）后打的。GNU tar 用 `--transform` 改成员名，
   排除列表比 macOS 那份长（从源码树直接打，开发脚手架还没被 rsync 排掉）。
2. **先刷新 `~/go1.27.1-ohos`**，它与仓库的差只剩三处：⑤ 的 amd64 外链 delta、
   `VERSION` 的 `-ohos` 标记、以及 10 个上游签入的二进制 testdata
   （`src/cmd/objdump/testdata/go116.o`、`src/go/internal/{gccgoimporter,gcimporter}/testdata/*.a`）。
   **arm64 不受影响** —— 实测用它编 cgo 可执行文件拿到正确的 `Type: DYN` + musl 解释器，
   所以这不是「已发布的 beta1 坏了」。但这些差异**不该进下一个 tarball**。
   → **已刷新**：按 §6.7 走 `rm -rf bin pkg` + `make.bash` + 手装 `-exec` 包装，
   三连验收绿（含设备侧 `ok strings 24.874s`）。
3. **打包源必须是从仓库 `make.bash` 出来的树**，不是已装树 —— 否则 ⑧ 那类
   「只在产物里对、仓库里没有」的手工改动会继续以不可复现的方式往下传。
   → **已守**：两份包都出自「从仓库同步 + `rm -rf bin pkg` + `make.bash`」的树。
   验收硬判据：**整棵 `bin/`（含手装的 `_exec` 包装，不只是 `bin/go`）**里
   「绝对路径形态的 `.go` 串」计数为 **0**（release 标志在）。
   **写 `bin/go` 单文件的版本漏掉了 7c** —— 那天两棵树的 `bin/go` 逐字节相同，
   只有包装不一样。判据与可移植写法见测试方案 §5.8。

**这个不对称已消除（2026-09-24）** —— 两层当初都在**beta1 那棵树上**测，beta2 时**都没有**重跑。
B 层 2026-09-23 补做，C 层 2026-09-24 补做，**两次重跑各抓到一个只在产物里出现的真缺陷**：
B 层抓到 **7c**（包装嵌构建机绝对路径 → 12 个包假红），C 层抓到 **7d**
（包装往 stderr 那行通知 → 交付树上第一个跑到的用例凭空失败）。
两次的修复都验证在产物上：B 层 262 行 / 240 PASS / 22 FAIL；C 层 2739 RUN / 132 FAIL。

**「跑出结论的那个包 = 用户拿到的包」这条对 beta2 是先不成立、同日重打后成立的**：
首版资产是修复前的，重跑用的是修复后的树（见 7c）；2026-09-23 决定**不换 tag、同名重打两份资产**，
打包源就是跑出 240/22 的那棵树（指纹 `743723f3…` —— 指纹只算 `bin/` 顶层，文档改动不进）。
所以那条成立是靠**重打**换来的，不是靠指纹自动保证的：指纹只是让这件事**可查**，不能让它被省略。
**本次 `v1.27.1-ohos` 同理**：因为 7d，两份资产又重打了一次，打包源 = 跑过 B 层**和** C 层的那棵树
（指纹 `bc958b2dca13…` / `3e8b4ece4304…`）。

> **口径提醒（本次踩到）**：指纹算**整棵 `bin/`**，含 `_exec` 包装，而 7c/7d 修的正是包装 ——
> 所以**换包装必然换指纹**。要论证「换包没换编译器」，得落到单个 `bin/go` 的 sha256 上
> （本次新旧两份逐字节相同，`9cfddd979ec1c64f…`）。详见测试方案 §5.9 与 §5.10。

> 这条值得单独记一句：**开发树上全绿的那一轮，和产物上的那一轮，结论不一样。**
> 两轮差的就是 tarball 里那个 `_exec` 包装 —— `bin/go` 逐字节相同。
> 所以「在交付物上重跑」不是形式主义，它是唯一能看见这类缺陷的动作。

**本 cut 顺手补的一个真缺口**：CI 此前只验「能编**出**工具链」，不验「打**出来**的包能用」。
`build.go` 的 release 闸门（`-trimpath` 等）此前**没有任何自动防线** —— 它失效的症状是
`bin/go` 里嵌着构建树绝对路径，而 `make.bash` 退出码照样是 0（§6.8 那条踩过的坑）。
已在 `.github/workflows/ohos.yml` 加「打包自检」：打包 → 解到临时目录 → 版本串带 `-ohos`
→ **整棵 `bin/` 的绝对路径为 0** → **从解出来的树**编一个 arm64 产物。

> **这条自检有个已知盲区，别当成它守住了包装。** CI 跑的是普通 `make.bash`，
> 而 `wrapperPathFor` 在 `goos == gohostos` 时返回空（§4.2）——
> **CI 那棵树里根本没有 `go_openharmony_*_exec`**，所以它查不到包装，也就查不出 7c。
> 包装是 §6.7 手装的，它的「不带构建机路径」性质由 **§6.7 打 tarball 前那条手工检查**守着。
> 哪天想让 CI 真覆盖它，得让 CI 走一次 `GOOS=openharmony ./make.bash`（交叉自举，
> 现在没跑过，代价是每轮多几分钟）。**在那之前，7c 这类缺陷的防线在文档里，不在 CI 里。**

---

## ⑩ 下一次 cut 的待办（2026-09-23 定，同日更新）

1. **~~B 层要在「发布树」上重跑，而现有驱动会假装已经跑过了。~~ 已闭环（2026-09-23）。**
   `misc/openharmony/runtests.sh` 的每条结论现在都带**整棵 `bin/` 的 sha256**，
   跳过只在指纹相同时生效；`--selftest` 里有 4 例指纹断言钉住它
   （含「只改 `go_*_exec` 也要换指纹」和「`bin/openharmony_*` 缓存不许影响指纹」）。
   指纹定义与 soak 文档第 0 节那条命令逐字对上（实测同一个数 `743723f32866…`）。

   > **指纹只取 `bin/` 顶层文件，不是整棵子树**（踩过）：`bin/openharmony_arm64/` 是包装按需
   > 编出来的缓存，跑起来才长出来 —— 算进指纹的话每次续跑都换指纹、把上一轮的 PASS 全作废，
   > 正好废掉这个脚本存在的理由。

   **但「在交付物上跑一遍」这个动作又挖出一个真缺陷**（beta2 首版资产带着它）：
   首版 tar 包里的 `bin/go_openharmony_*_exec` 嵌着构建机绝对路径，
   导致解包到别处的树上有 12 个包在设备侧假红。根因、机制、修法与验收见
   `docs/ohos-release-notes-v1.27.1-beta2.md` **已知问题 7c**。
   ⇒ **已按 2026-09-23 的决定同名重打两份资产（tag 不动），打包源 = 跑出 240/22 的那棵树**；
   下一个 cut 起沿用这条纪律：**打包源必须是跑过 B 层**（2026-09-24 起还要加上 **C 层**）
   **的那棵树**，指纹对不上就不能复用产物。
   **C 层那一轮又抓到一个同源的**（7d，2026-09-24 修，见 ⑩.4）—— 两次都是「开发树比交付树多带状态」，
   所以这条纪律不是形式主义，它现在**两发两中**。
   连带修掉的还有一处同源问题：`§6.7` 的 rsync 排除项 `pkg/` 没锚定，
   会静默丢掉 11 个上游签入的 testdata（已改 `/pkg/`，并加了树间存在性检查）。

2. **linux 那台（238）没有 OHOS SDK**，所以那边的 tarball 验收只覆盖到「arm64 产物正确 +
   amd64 报错正确」，**amd64 的真产物形状在 Linux 侧一次都没验过**（只在 macOS 上验过）。
   要补就装个 commandline-tools 到 238；不补也不算盲区，因为 amd64 的构建路径与宿主无关。

3. **宿主基线要在 2026-10-19 之后重认一次。** `ubuntu-latest` 从那天起迁到 Ubuntu 26，
   而 A 层基线（377 ok / 0 FAIL）是在 24.04 上量的。CI 里两格 runner 已**写死版本**
   （`ubuntu-24.04` / `macos-14`），所以漂移不会突然把 CI 弄红 —— 它会**安静地不生效**：
   写死的版本迟早被 GitHub 下线。届时改那一行 + **重量 A 层基线**，别只改版本号就完事。
   期间新增的「宿主身份」步骤（`/etc/os-release` + `uname -a` + `cc --version` + `go version`）
   是唯一能事后回答「当时是哪台机器」的东西。

4. **~~C 层还没在交付物上重跑过~~ 已闭环（2026-09-24），并当场抓到第二个交付物专属缺陷。**
   驱动 `ohos-test-results/c-layer-release-tree-v1.27.1-ohos/driver.sh`，
   树 = 本次两份资产的打包源（`~/tmp/gate3/go1.27.1-ohos`）。
   结果回答了这个待办写下的那个问题：**那 125 条在新树上仍是同一条链接器错误** ——
   RUN **2739** / FAIL **132**，与 §5.7 开发树的两个数**逐字相同**。

   | 分片 | 修前交付树 FAIL | 修后 | RUN |
   |---|---|---|---|
   | 0 | 41 | **40** | 696 |
   | 1 | 29 | 29 | 674 |
   | 2 | 29 | 29 | 664 |
   | 3 | 34 | 34 | 705 |
   | 合计 | **133** | **132** | **2739** |

   修前那 133 的构成 = **125**（`R_ARM64_TLS_IE`）+ **7**（§5.7.3 逐条同名同签名）+ **1**。
   多出来的那条是 `Test/abi/open_defer_1.go`，它捕获的输出就是包装那行通知的原文
   —— 即已知问题 **7d**（详见 release notes）。**分片差恰好是这 1 条**（只有 shard 0 从 41 落到 40），
   与 7d 的机理对上：那行只出现一次（编完即止），所以**只有第一个跑到的用例中招**，
   修前 133 这个数在交付树上**不可复现**。

   **代价实测 6 分 10 秒**（原来 §6 估的「2–5 小时」不成立，已更正）——
   所以这条纪律以后不必权衡，**每次 cut 整轮重跑即可**。
   全过程见测试方案 **§5.10**。

5. **真机长稳要升级成「非 beta」措辞，还差三件**（2026-09-23 记，**不是 cut 阻塞项**）。
   §5.9 那一轮 30 分钟，只够排除**快速**泄漏；「长稳」这个词要站得住，得补：
   ① **小时级**（协议不用重设计，改时长即可，协议页在 `~/tmp/ohos-go-soak-验证要求.md`）；
   ② **冻结 → 解冻**（这一轮 OHOS 根本没冻结那个 VPN 常驻进程 —— 后台一直在收数据 ——
   这条真机独有的失效路径**没走到**）；
   ③ **16 KB 页设备**（现有真机与模拟器全是 4 KB / 39 位，这个悬案至今无人能答）。
   另有两条**载体侧因无 root 取不到**的：`fd` 数、`faultlog`。
   代价很低（下游已有真机夹具），**卡的不是能力是排期** —— 谁要发正式版，先把这三件点掉。

6. **交付前 review 在同一个包装文件里又挖出 5 条，已修；两层证据按新树重取**（2026-09-24，
   **发版暂缓**：用户要求先把这一类挖干净）。逐条见 release notes **7e**：
   H1 可选的「编目标架构工具链」失败会连带否决必须做的源码树镜像；
   H2 设备侧解包用 `;` 串联且**无条件**写同步指纹（失败被永久缓存，重试本可答对却答错）；
   M1 降级通知漏了 `noteToHuman`（**就是 7d 那条收口漏掉的另一半**）；
   M2 `cd` 失败被伪装成「测试退出码 1」；M3 拼进 shell 串的设备路径没加引号。
   本包用例 **4 → 8**（新增 `devicecmd_test.go`），7 处变异全部被抓住。

   **证据重取**（包装就是执行设备测试的载体，旧的证据已经不属于这个交付物）：
   C 层 **2739 / 132，与上一轮逐行相同**（连 132 个 FAIL 名单 `diff` 都是空的，见测试方案 **§5.11**）；
   B 层 **240 / 22**，262 行跳过 0、TIMEOUT/WRAPPER 0/0，**22 个 FAIL 名单排序后 `diff` 为空**
   （见 **§5.12**；耗时 3h19m，落在 §6 估的 1.5–3 小时上沿偏外 —— **B 层不能整轮重跑，续跑纪律照旧**）。
   树指纹 `bc958b2d…` → `d681e0a3…`，而 **`bin/go` 逐字节未动**
   （`9cfddd97…`，与 C 层交付物 MANIFEST 记的相同）—— **指纹变的是包装，不是编译器**；
   要论证「改的没碰编译器」用 `bin/go` 的单值 sha256，别用指纹（那里面含包装）。

   **文档/判据侧同时收口的（都是 review 的第二批）**：
   §6.8 的验收从一条「必须为空」扩成**五条独立判据** —— 旧版那条把门命令用的
   `grep -c "$(pwd)"` 正是同一份文档在 §6.7 否定过的判据（**解包后恒为 0**）；
   Linux 打包配方补上「源码树要先装 `_exec` 包装」（此前依赖「那棵树上恰好装过」，
   换棵新树就会静默漏掉，两份已发 Linux 资产是碰巧带上的）；`runtests.sh` 补 SDK 存在性门禁
   （SDK 缺席会让每个 cgo 包 FAIL，而 FAIL 的形状和 port 缺陷一模一样）—— **这条门禁必须排在
   设备探活之前**：238 那台既没 SDK 也没 hdc，排后面就死在「模拟器不可达」上，把人支去重启一台
   永远不会跑 B 的机器的模拟器。已在 238 实测（`OHOS_SDK=/nonexistent` → `rc=2` + 正确报错）。

   > **新记住的一条**：**包装的降级路径在 `go test` 下是看不见的。** 它被 `testdir`
   > 当成被测二进制，stderr 被并进比对缓冲区 —— 于是多写一个字是假红、全关掉又让交互使用变哑。
   > 所以那 5 条覆盖的全是「正常路径永远不会走到」的分支，只能靠**单独跑包装自己的测试**
   > 去逼出来。以后改这个文件，先看它的降级分支有没有断言。

---

## 明确不修（写清即可，别当待办）

| 项 | 原因 |
|---|---|
| `-race` | 39 位 VMA vs Go race runtime 的 48 位布局。**是决策，不是欠债** —— 开发期诊断工具，产物不带；上游立场是「平台自己调 VMA」。四层闸与替代路径见 `docs/go-upgrade-guide.md` §4.2 |
| `-msan` | SDK 无 `libclang_rt.msan*`，且要插桩过的 libc |
| 真机 `sh` 域 exec | 未签名二进制被拒，换加载器绕过也无效；只影响*测试方式*，不影响产物 |
| `hdc shell` NUL 截断 | 只影响「测试输出含 NUL」；要修得改执行路径核心 |
| x509 用户自装 CA | 应用域读不到 `/data/certificates/user_cacerts`；要做得走 OHOS cert framework + cgo，是真特性（见 ①） |

---

## 一条反向的坦白：覆盖面看着厚，运行期其实薄

- C 层 125 条 FAIL 挡在**链接期** —— 「编译发生了，运行没有」（`ohos-full-test-plan.md` §5.7）
- A 层**已重跑**（见 ②），但它证明的是「宿主编译器没坏」，与设备运行期无关

所以「**编译正确性**」证据很厚，「**运行期正确性**」主要来自 B 层那一轮 + 环回 HAP + xray 端到端。
这个不对称该写进 release notes，别让它看起来像「全面验证过」。

**2026-09-23 加了第五格**：下游载体在**真机、应用域、真实流量**上跑了 30 分钟
（`ohos-full-test-plan.md` §5.9），且工具链指纹与 **darwin 那份交付资产**同源（指纹是 `bin/`
顶层哈希，Linux 那份的 `bin/` 是另一组二进制，本来就不是同一个值）——
这是本仓库到现在为止**唯一一条由外部产出的、能溯源到交付物**的运行期证据。
它把不对称往前挪了一格，但**没有消除**：30 分钟只够排除快速泄漏，
`fd` 与 `faultlog` 因设备无 root 整项取不到，**16 KB 页**和**冻结→解冻**两条路径仍未覆盖。
⇒ 非 beta 的「长稳」措辞需要小时级 + 那两项补测，见 ⑩.5。

> **2026-09-24 收紧：别把这句读成「指纹对得上新包」。** `743723f3…` 是 **beta2 那份**的 `bin/` 指纹；
> 修 7d 重装包装后同一棵树变成 `bc958b2d…`。但真正要保的那件事没变，且可以说得更硬：
> 两份资产的 **`bin/go` 与 `bin/gofmt` 逐字节相同**（`9cfddd979ec1c64f…`），
> 唯一的差是宿主侧 `-exec` 包装（`8dd25d03…` → `668bb3be…`），而 v2rayHM 那条
> ArkTS → N-API → cgo → c-shared 的路径**不经过包装**。
> ⇒ 通用教训：**指纹是「整棵 `bin/`」的便利口径，不是「谁对编译结果负责」的精确口径**；
> 要论证「换包没换编译器」，得落到单个 `bin/go` 的哈希上。详见 §5.9 的收紧注。
