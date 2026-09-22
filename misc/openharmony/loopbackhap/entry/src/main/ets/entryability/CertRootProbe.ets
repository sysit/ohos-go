/*
 * CertRootProbe — 应用域能不能读到系统 CA 根？
 *
 * 存在理由：B 层全量轮里 `crypto/x509` FAIL，唯一那条报错是
 *
 *     cert_pool_test.go:20: open /etc/ssl/certs: permission denied
 *
 * 出自 `TestCertPoolEqual` —— 而这个测试的**主题是证书池的相等语义**，
 * `SystemCertPool()` 只是它的前置步骤（失败即 t.Fatal）。所以这条 FAIL 说明的是
 * 「SystemCertPool() 在设备上返回错误」，**不是**「x509 在 OHOS 上坏了」。
 *
 * 而 `SystemCertPool()` 有三种结局，对下游的意义完全不同（loadOnDiskRoots，
 * crypto/x509/root.go:153）：
 *
 *   /etc/ssl/certs 可读且有 PEM  → 返回系统根        → 下游 HTTPS ✅
 *   /etc/ssl/certs EACCES        → 返回 error        → 下游 HTTPS ❌ 响亮失败（当前）
 *   /etc/ssl/certs ENOENT        → 返回空池 + nil    → 下游 HTTPS ⚠️ 静默失败
 *                                    （roots.len()==0 && firstErr==nil 走 root.go:199）
 *
 * 第三种最坏：不开报错，等 TLS 握手才报 unknown authority。这条夹具就是要把三态分开。
 *
 * 为什么 `sh` 域测不出来：同一个报错在 `sh`(uid 2000) 下已被证实。
 * 但环回那件事刚刚证明 `sh` 域的结论不可外推 —— 同一个 bind，`sh` 域拒、
 * 应用域五个探针全过。所以必须换到应用域再问一次。
 *
 * 顺带一个从 `sh` 域报错串反推出来的事实（值得记）：报出来的是**目录** `/etc/ssl/certs`
 * 而不是某个 `certFiles` 条目，说明前 6 个 `certFiles` 全返回了 ENOENT（否则 firstErr
 * 会被更早的条目占住）。`open` 能返回 ENOENT 说明 `/etc` **可搜索**；那 `ls /etc` 被拒
 * 就只能是**可搜索但不可读**（x 有、r 无）。而 `/etc/ssl/certs` 拿到 EACCES 而非 ENOENT
 * ⇒ 该目录**存在**，只是读不了。结论成立，但这条链子得写出来别人才复核得了。
 *
 * 读系统路径走 SELinux 策略，不走 requestPermission —— 所以 module.json5 一个字没改。
 */

import { fileIo as fs } from '@kit.CoreFileKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { BusinessError } from '@kit.BasicServicesKit';

const DOMAIN: number = 0x9b01;
const TAG: string = 'CertProbe';

// 够看清 PEM 的 `-----BEGIN CERTIFI` 就够了。
const HEAD_BYTES: number = 24;

// 列目录时最多报几个条目 —— 只要证明「列得出来」，不要刷屏。
const MAX_ENTRIES: number = 6;

/*
 * 候选路径，按「回答能力的顺序」排：
 *   /etc                          只测可搜索 vs 可读的分界（预期不可列）
 *   /etc/ssl/certs                Go 的 certDirectories[0]，当前 EACCES 的来源
 *   /etc/ssl/certs/cacert.pem     镜像里的主 bundle —— **不在** Go 的 certFiles 里
 *   /etc/ssl/cert.pem             在 Go 的 certFiles 里（root_linux.go:16，注释写 Alpine）
 *   /system/etc/ssl/certs/cacert.pem  镜像里 system 分区的那份
 *   /data/certificates/user_cacerts   OHOS 用户 CA（对应 Android 的 certs-added）
 */
const CANDIDATES: string[] = [
  '/etc',
  '/etc/ssl/certs',
  '/etc/ssl/certs/cacert.pem',
  '/etc/ssl/cert.pem',
  '/system/etc/ssl/certs/cacert.pem',
  '/data/certificates/user_cacerts',
];

function log(msg: string): void {
  // %{public} 是刻意的：这里没有用户数据，而 private 会把内容打成 <private>，那就白测了。
  hilog.info(DOMAIN, TAG, '%{public}s', msg);
  console.info(`CertProbe: ${msg}`);
}

function describe(e: Object): string {
  const be = e as BusinessError;
  return `code=${be.code} message="${be.message}"`;
}

// 把开头的字节转成可读串，用来确认「这真是个 PEM」而不是碰巧能打开的空壳。
function toAscii(buf: ArrayBuffer, n: number): string {
  const bytes: Uint8Array = new Uint8Array(buf, 0, n);
  let s: string = '';
  for (let i = 0; i < bytes.length; i++) {
    const c: number = bytes[i];
    if (c >= 32 && c < 127) {
      s += String.fromCharCode(c);
    } else {
      s += (c === 10) ? '\\n' : '.';
    }
  }
  return s;
}

/*
 * 一个路径一条日志，三态分开：
 *   -> OPEN OK ...            可读
 *   -> code=13900012 ...      存在但被拒（Permission denied）
 *   -> code=13900002 ...      不存在（No such file）
 * OHOS 的 fileIo 错误码把 EACCES/ENOENT 分得很清楚，这正是 `[ -e ]` 那种探针做不到的
 * —— shell 里 `[ -e ]` 在父目录不可搜索时返回假，会把「读不到」误报成「不存在」。
 */
function probePath(label: string, path: string): void {
  let file: fs.File;
  try {
    file = fs.openSync(path, fs.OpenMode.READ_ONLY);
  } catch (e) {
    log(`${label} ${path} -> ${describe(e)}`);
    return;
  }
  // 走到这里 file 一定已赋值（上面 catch 里 return 了）。拆成两步是为了避开
  // ArkTS 对 `let file: fs.File | undefined` 的确定赋值分析。
  describeOpenedFile(label, path, file);
}

function describeOpenedFile(label: string, path: string, file: fs.File): void {
  try {
    // openSync 在 API 12+ 返回 File；要裸描述符得取 file.fd。
    const st: fs.Stat = fs.statSync(file.fd);
    let extra: string = '';
    if (st.isDirectory()) {
      const names: string[] = fs.listFileSync(path);
      const head: string = names.slice(0, MAX_ENTRIES).join(',');
      extra = ` entries=${names.length}[${head}${names.length > MAX_ENTRIES ? ',...' : ''}]`;
    } else {
      const buf: ArrayBuffer = new ArrayBuffer(HEAD_BYTES);
      const n: number = fs.readSync(file.fd, buf);
      extra = ` head="${toAscii(buf, n)}"`;
    }
    log(`${label} ${path} -> OPEN OK isDir=${st.isDirectory()} size=${st.size}${extra}`);
  } catch (e) {
    log(`${label} ${path} -> 打开了但 stat/read 失败 ${describe(e)}`);
  } finally {
    try {
      fs.closeSync(file);
    } catch (e) {
      // 收尾失败不影响任何结论，不打日志免得干扰判读。
    }
  }
}

/*
 * 补一个只有 lstat 能回答的问题：这些路径是不是 symlink？
 *
 * 因为 Go 的 readUniqueDirectoryEntries（root.go:206）会**跳过目标串里不含 `/` 的 symlink**
 * （isSameDirSymlink，root.go:224）。若 `/etc/ssl/certs/cacert.pem` 恰好是「同目录软链」，
 * Go 就会把它剔掉；而该目录下**只有它一个条目**（探针 B）→ 空池 + nil error，
 * 正是上面三态里最坏的那一格。A–F 全 OK 排除不了这条 —— 上面的 open 会跟着软链走，
 * 看起来一模一样。所以单独问一次。
 *
 * ArkTS 的 fs 没有 readlink，只有 lstat —— 够用：只要它不是 symlink，
 * isSameDirSymlink 必然返回 false，问题就不存在。
 */
function probeSymlinks(): void {
  for (let i = 0; i < CANDIDATES.length; i++) {
    const path: string = CANDIDATES[i];
    const label: string = `S${String.fromCharCode(65 + i)}`;
    try {
      const st: fs.Stat = fs.lstatSync(path);
      log(`${label} lstat ${path} -> symlink=${st.isSymbolicLink()}`);
    } catch (e) {
      log(`${label} lstat ${path} -> ${describe(e)}`);
    }
  }
}

export function probeCertRoots(): void {
  log('=== CertProbe start ===');
  for (let i = 0; i < CANDIDATES.length; i++) {
    probePath(String.fromCharCode(65 + i), CANDIDATES[i]);
  }
  probeSymlinks();
  log('判读：B 可读→Go 上游 certDirectories 已覆盖 OHOS，B 层那条 FAIL 是 sh 域假象；');
  log('      B=EACCES→与 sh 域同因，换应用域仍失败，需给 OHOS 补路径；');
  log('      B=ENOENT→空池静默失败，最坏，必须补路径。');
  log('=== CertProbe DONE ===');
}
