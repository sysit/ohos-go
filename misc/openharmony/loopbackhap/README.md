# loopbackhap —— 应用域的权限边界探针

这个夹具回答**两个**都只能靠「换个权限域再问一次」才能回答的问题：

1. 应用域能不能 bind/listen `127.0.0.1`？（`LoopProbeAbility`）
2. 应用域能不能读到系统 CA 根？（`CertProbe`，见下半篇）

两个问题的共同形状：**同一个操作，`sh` 域失败、应用域成功** ——
所以「设备上做不到」这个结论在只观察过 `sh` 域时是不成立的。

## 问题一：应用域能不能 bind 127.0.0.1？

B 层全量（262 个有测试的 std 包交叉编译 + 设备执行）里有 **17 个包**（`net`、`net/http`、
`internal/trace`、`os/exec` 一族……）的 FAIL 都指向同一个症状：用例要在 `127.0.0.1`
上起监听，拿不到端口。设备侧报出来的是 nettest 掩盖过的
`tcp is not supported on linux/arm64`，看不出真正的 errno。

当时的候选解释有两条，**它们对「要不要发 beta」的结论完全相反**：

1. **`sh` 权限域限制** —— 设备上跑测试的是 `uid=2000(shell)` / `context=u:r:sh:s0`；
   OHOS 的 SELinux 策略不给 shell 域 `node_bind`，所以是**跑测试的方式**受限。
2. **port 缺陷** —— 我们的移植让 `net` 的监听路径在 OHOS 上坏了。

第 2 条会**阻塞 beta**（下游 v2rayHM / xray 全靠应用内听 `127.0.0.1` 做本地代理）。
区分两条的办法只有一个：**换一个权限域去 bind**。这个 HAP 就是那个「换域」的最小载体 ——
应用域是 `uid=200200xx`、`u:r:app:s0`，与 shell 域不是同一套策略。

## 它做什么

一个**刻意无窗口**的 `UIAbility`（`LoopProbeAbility`，不调 `loadContent`），在
`onWindowStageCreate()` 里用 `@ohos.net.socket` 依次跑五个探针，结果只走 hilog：

| 探针 | 动作 | 意义 |
|---|---|---|
| A | `bind 127.0.0.1:0` | 环回 IPv4 能不能拿到端口（随机端口，排除占用干扰） |
| B | `bind ::1:0`（family 2 = IPv6） | 环回 IPv6 |
| C | `bind 0.0.0.0:0` | 通配地址 |
| D | `listen 127.0.0.1:39321` | 固定端口 + 真正 `listen` |
| E | 对自己 `connect 127.0.0.1:39321` | **证明 D 是真 listener**，不是只 bind 成功 |

只有 E 成功才排除了「bind 能返回但没真监听」这种假绿。
`hilog` tag 是 `LoopProbe`，域 `0x9b01`。

## 怎么跑

```bash
DEVECO=/Applications/DevEco-Studio.app/Contents
export PATH="$DEVECO/tools/node/bin:$PATH"          # hvigorw 要 node

cd misc/openharmony/loopbackhap
"$DEVECO/tools/ohpm/bin/ohpm" install --all
"$DEVECO/tools/hvigor/bin/hvigorw" --mode module -p product=default \
    -p buildMode=debug assembleHap --no-daemon

hdc install -r entry/build/default/outputs/default/entry-default-unsigned.hap
hdc shell aa start -a LoopProbeAbility -b com.9bt.ohosloop
hdc shell hilog -x | grep -E 'LoopProbe|CertProbe'   # 两组探针，或先 hilog -r 清缓冲
```

**模拟器上不需要签名。** 上表那条 `hdc install` 装的是 `-unsigned.hap`，
模拟器照收（`install bundle successfully`）。`hvigorw` 只会对缺失的
`signingConfigs` 报一句 WARN 然后跳过签名。**这是模拟器的性质，不是 HarmonyOS 的保证** ——
商用真机只信华为 CA，换真机要重新走签名（`~/.ohos/config/` 里那套 `*.p12/*.cer/*.p7b`）。

事后清理：

```bash
hdc shell aa force-stop com.9bt.ohosloop
```

## 期望输出与判读

```
LoopProbe  A bind 127.0.0.1:0 OK -> local 127.0.0.1:39957
LoopProbe  B bind ::1:0 OK -> local ::1:60747
LoopProbe  C bind 0.0.0.0:0 OK -> local 0.0.0.0:58509
LoopProbe  D listen 127.0.0.1:39321 OK
LoopProbe  E connect 127.0.0.1:39321 OK
LoopProbe  E accepted inbound connection clientId=1 -> 环回 listener 真的能收
LoopProbe  === LoopProbe DONE ===
```

- **A~E 全 OK** → 那条 17 包 FAIL 类**已定性为 `sh` 权限域限制**，不是 port 缺陷。
  结论落在 `docs/ohos-full-test-plan.md` §5.7 与 `docs/go-upgrade-guide.md` §4.2。
- **A 或 D 失败** → 上面的分类是错的，需要重新定性，**它会阻塞 beta**。
  把 A 的 `describe(e)` 全文（`code`/`message`）、`hdc shell id`、以及
  `hdc shell hilog | grep avc` 一起留下来再判断。
- **B 失败但 A 通过**（只 IPv6 环回不行）→ 记成独立一条，不影响 A/D 的结论。

**确认跑在应用域里**：`hdc shell "ps -ef | grep ohosloop"` 应显示 `u:r:app:s0` 与
`200200xx` 段的 uid（本机实测 `20020077`）。若显示的是 `2000(shell)`，那是启动方式错了，
探针结果不成立。

## 实测结果（2026-09-22，模拟器 `127.0.0.1:5555`）

五个探针全部 OK，进程 uid `20020077`（应用域）。独立旁证：同一个模拟器上
`com.9bt.transmissionbtm`（uid `20020076`）能 bind `0.0.0.0:51413` 与 `[fe80::…]:51413`。
**结论：17 包环回类是 `sh` 权限域限制，非 port 缺陷。**

## 问题二：应用域能不能读到系统 CA 根？

### 为什么问

B 层 `crypto/x509` FAIL，唯一报错是 `cert_pool_test.go:20: open /etc/ssl/certs: permission denied`
（出自 `TestCertPoolEqual` —— 它的**主题是池子相等语义**，`SystemCertPool()` 只是被 `t.Fatal`
护着的前置步骤。**所以这条 FAIL 说明的是「设备上取不到系统根」，不是「x509 坏了」**）。

release notes 当初据此让下游设 `SSL_CERT_FILE`，但那个处方有个已知硬伤：**包装不下发宿主 env**。
而更要紧的是另一个可能 —— `SystemCertPool()` 有三种结局，对下游意义完全不同
（`crypto/x509/root.go:153` `loadOnDiskRoots`）：

| `/etc/ssl/certs` | 结果 | 下游 |
|---|---|---|
| 可读且有 PEM | 返回系统根 | HTTPS ✅ |
| EACCES | 返回 error | HTTPS ❌ **响亮失败**（当时的状态） |
| ENOENT | **空池 + nil** | HTTPS ⚠️ **静默失败**（`roots.len()==0 && firstErr==nil` 走 `root.go:199`） |

第三格最坏：不报错，等 TLS 握手才报 unknown authority。而 `sh` 域观测到的 EACCES
（或 ENOENT）**不能外推** —— 环回那件事已经证明同一操作在两域结论相反。

### 探针

`entry/src/main/ets/entryability/CertRootProbe.ets`，在环回那组跑完后由
`LoopProbeAbility.runProbes()` 调起（同一进程、同一域）。逐路径 `open` 并分三态报出，
再对同一批路径 `lstat` 一次：

| 探针 | 路径 | 问什么 |
|---|---|---|
| A | `/etc` | 可搜索 vs 可读的分界 |
| B | `/etc/ssl/certs` | **Go 的 `certDirectories[0]`**，EACCES 的来源 |
| C | `/etc/ssl/certs/cacert.pem` | 镜像里的主 bundle（**不在** Go 的 `certFiles` 里） |
| D | `/etc/ssl/cert.pem` | 在 Go 的 `certFiles` 里（`root_linux.go:16`） |
| E | `/system/etc/ssl/certs/cacert.pem` | system 分区那份 |
| F | `/data/certificates/user_cacerts` | OHOS 用户 CA（对应 Android 的 `certs-added`） |
| S* | 同上各路径 | `lstat`：**是不是 symlink**（见下，这条是隐藏杀手） |

**为什么必须单独问 symlink**：Go 的 `readUniqueDirectoryEntries`（`root.go:206`）会跳过
**目标串里不含 `/` 的 symlink**。而 `/etc/ssl/certs` 下**只有 `cacert.pem` 一个条目** ——
若它是同目录软链，Go 会把它剔掉 → 空池 + nil error，正是上表最坏那格。
A–F 的 `open` 会跟着软链走，**看不出来**。
（ArkTS 的 `fs` 没有 `readlink`，只有 `lstat` ——够用：不是软链，问题就不存在。）

### 实测结果（2026-09-22，模拟器 `127.0.0.1:5555`，uid `20020077`）

```
A /etc                        -> OPEN OK isDir=true entries=202[CollaborationFwk,...]
B /etc/ssl/certs              -> OPEN OK isDir=true entries=1[cacert.pem]
C /etc/ssl/certs/cacert.pem   -> OPEN OK isDir=false size=191450 head="-----BEGIN CERTIFICATE--"
D /etc/ssl/cert.pem           -> code=13900002 No such file or directory
E /system/etc/ssl/certs/cacert.pem -> OPEN OK isDir=false size=191450 head="-----BEGIN CERTIFICATE--"
F /data/certificates/user_cacerts  -> code=13900012 Permission denied
SC lstat /etc/ssl/certs/cacert.pem -> symlink=false
SF lstat /data/certificates/user_cacerts -> symlink=false
SA lstat /etc                 -> code=13900012 Permission denied   ← 但 A 读得到，见下
```

**结论：应用域读得到系统根，Go 上游默认路径本来就覆盖 OHOS —— 不需要任何 env。**
逐环对齐（`root.go:183-197`）：`ReadDir` 成功（B）→ 条目非软链（SC）→ `ReadFile` 成功（C）
→ 同一份字节在宿主上 `AppendCertsFromPEM` **125/125 成功** → `roots.len() > 0` → `root.go:199` 返回池子。

上游 6 个 `certFiles` 在 OHOS **全 ENOENT**（探针 D 是一个样本；ENOENT 被 `os.IsNotExist` 忽略，
不占 `firstErr`），但 `certDirectories[0] = "/etc/ssl/certs"` 正好命中。

**顺带证伪了「x509 需设 `SSL_CERT_FILE`」那条处方** —— 那是把 `sh` 域限制当成了平台限制。

**真正的缺口只剩一条**：`F` 得 EACCES（而 `SF` 的 `lstat` 成功 ⇒ 该目录**存在**，只是不给应用读）
→ **用户自装 CA 取不到**。上游给 Android 加的两行（`root_linux.go:26-30`）照抄无效，
因为问题不是路径不对是权限不给。要支持得走 OHOS cert framework + cgo，是真特性不是补丁。

`SA` 得 EACCES 而 `A` 读得到，看似矛盾，其实不是：`lstat("/etc")` 要的是 `/etc` 自身的 `getattr`，
`open("/etc")` 要的是 `search`+`open`+`read`，OHOS 的策略在这两者上不同。**Go 不 lstat `/etc`**，无关结论。

**没做的**：没在应用域里跑真的 Go 二进制。证据是逐 syscall 对齐的，不是端到端的 ——
剩下的推断只有「ArkTS 的 `open` ≡ Go 的 `open`」。要端到端铁证得建 native 载体
（HAP + Go c-shared `.so` + N-API 胶水），判断是不值得。

两组探针在**同一次** `aa start` 里串行跑完（`LoopProbeAbility.runProbes()` 尾部调
`probeCertRoots()`），所以构建、安装、启动都只有一次。

## 工程文件是怎么来的（别手写那两个 json5）

`build-profile.json5`、`entry/build-profile.json5`、`oh-package.json5`、
`hvigorfile.ts`、`hvigor/hvigor-config.json5` 是从 DevEco 自带的脚手架**原样拷**的，
不是手写的：

- `plugins/codegenie-plugin/previewProject/` —— 整个工程骨架
- `plugins/codegenie-plugin/previewProjectTemplate/oh-package.json5` —— 注意用**这个**，
  `previewProject` 那份的 `@ohos/hamock@1.0.1-rc2` 已经不在 registry 里，
  `ohpm install` 会 `NOTFOUND`。模板那份的 `modelVersion` 同为 `5.0.0`，
  依赖/devDependencies 都是空的。

本仓库有一条 hook 禁止直接改这两个 `*.json5`（要改走 DevEco 的项目结构面板），
拷脚手架是既合规又不易错的路子。`local.properties`、`oh_modules/`、`build/`、
`.hvigor/` 都已在 `.gitignore` 里。

## 已知的无关干扰

- `hdc shell` 读不到应用沙箱（`/data/app/el2/...`），所以结果通道只能是 `hilog`。
- 视觉上它就是个白屏/无窗口应用 —— 这是故意的，见 `entry/src/main/ets/pages/Index.ets`
  的注释：加 UI 只会多一条与结论无关、却可能失败的路径。
