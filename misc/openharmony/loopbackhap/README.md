# loopbackhap —— 应用域能不能 bind 127.0.0.1？

## 这个夹具回答什么问题

B 层全量（262 个 std 包交叉编译 + 设备执行）里有 **17 个包**（`net`、`net/http`、
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
hdc shell hilog -x | grep LoopProbe          # 或先 hdc shell hilog -r 清缓冲
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
