/*
 * LoopProbeAbility — 应用域能不能在 127.0.0.1 上 bind/listen？
 *
 * 存在理由：B 层全量轮里 17 个包的 FAIL 都是 `bind: permission denied`。设备侧
 * 执行测试的是 `hdc shell`，SELinux 域是 u:r:sh:s0；应用进程的域不同。所以
 * 「Go 的 net.Listen 在 OHOS 上做不到」与「sh 域不被允许 bind」是两回事，而
 * 后者完全不在 port 的控制范围内。这条 fixture 就是把这件事钉死。
 *
 * 为什么不能用别的方式验：应用沙箱（/data/app/el2/...）在 sh 域读不到，所以
 * 结果只能走 hilog —— 幸好 hilog 读得到。
 *
 * 五个探针，分开报，因为要回答的不止一个问题：
 *   A  bind 127.0.0.1:0       —— 与 Go 的 net.Listen("tcp","127.0.0.1:0") 同一件事
 *   B  bind ::1:0             —— 区分 v4/v6，net 包的失败里两者都有
 *   C  bind 0.0.0.0:0         —— 对照：全拒还是只拒环回
 *   D  listen 127.0.0.1:PORT  —— bind+listen（ArkTS 的 TCP server 形态）
 *   E  connect 127.0.0.1:PORT —— 自连一手，证明 D 的 listener 是真的能收
 *
 * A 成功即可定性；D+E 成功则连「能收」都验了。C 失败而 A 成功是最有意思的
 * 组合（说明策略是按地址而非按域区分）。
 */

import { UIAbility } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { socket } from '@kit.NetworkKit';
import { BusinessError } from '@kit.BasicServicesKit';

const DOMAIN: number = 0x9b01;
const TAG: string = 'LoopProbe';

// 自己挑一个高位端口，避开设备上系统服务常用的那几个。
const PROBE_PORT: number = 39321;

// 给自连的 accept 回调留出落地时间。
const ACCEPT_GRACE_MS: number = 1500;

function log(msg: string): void {
  // %{public} 是刻意的：这里没有用户数据，而 private 会把内容打成 <private>，
  // 那就白测了。
  hilog.info(DOMAIN, TAG, '%{public}s', msg);
  console.info(`LoopProbe: ${msg}`);
}

function describe(e: Object): string {
  const be = e as BusinessError;
  return `code=${be.code} message="${be.message}"`;
}

async function probeBind(label: string, address: string, port: number, family?: number): Promise<void> {
  const sock: socket.TCPSocket = socket.constructTCPSocketInstance();
  const what: string = `${label} bind ${address}:${port}`;
  try {
    const net: socket.NetAddress = family === undefined
      ? { address, port }
      : { address, port, family };
    await sock.bind(net);
    const local: socket.NetAddress = await sock.getLocalAddress();
    log(`${what} OK -> local ${local.address}:${local.port}`);
  } catch (e) {
    log(`${what} FAILED ${describe(e)}`);
  }
  try {
    sock.close();
  } catch (e) {
    // close 失败不影响结论，但打出来，免得它悄悄变成下一条的干扰项。
    log(`${what} close FAILED ${describe(e)}`);
  }
}

async function probeListenBind(): Promise<socket.TCPSocketServer | undefined> {
  const srv: socket.TCPSocketServer = socket.constructTCPSocketServerInstance();
  const what: string = `D listen 127.0.0.1:${PROBE_PORT}`;
  try {
    await srv.listen({ address: '127.0.0.1', port: PROBE_PORT });
    log(`${what} OK`);
    srv.on('connect', (client: socket.TCPSocketConnection) => {
      log(`E accepted inbound connection clientId=${client.clientId} -> 环回 listener 真的能收`);
    });
    return srv;
  } catch (e) {
    log(`${what} FAILED ${describe(e)}`);
    try {
      srv.close();
    } catch (err) {
      log(`${what} close FAILED ${describe(err)}`);
    }
    return undefined;
  }
}

async function probeSelfConnect(): Promise<void> {
  const c: socket.TCPSocket = socket.constructTCPSocketInstance();
  const what: string = `E connect 127.0.0.1:${PROBE_PORT}`;
  try {
    await c.connect({ address: { address: '127.0.0.1', port: PROBE_PORT }, timeout: 3000 });
    log(`${what} OK`);
  } catch (e) {
    log(`${what} FAILED ${describe(e)}`);
  }
  // 连接留着不关：先让 accept 回调落地，再由调用方收尾。
  await new Promise<void>((resolve) => setTimeout(resolve, ACCEPT_GRACE_MS));
  try {
    c.close();
  } catch (e) {
    log(`${what} close FAILED ${describe(e)}`);
  }
}

export default class LoopProbeAbility extends UIAbility {
  onWindowStageCreate(): void {
    log('=== LoopProbe start ===');
    this.runProbes().catch((e: Object) => {
      log(`LoopProbe aborted ${describe(e)}`);
    });
  }

  private async runProbes(): Promise<void> {
    await probeBind('A', '127.0.0.1', 0);
    await probeBind('B', '::1', 0, 2);
    await probeBind('C', '0.0.0.0', 0);

    const srv: socket.TCPSocketServer | undefined = await probeListenBind();
    if (srv !== undefined) {
      await selfConnectThenClose(srv);
    }
    log('=== LoopProbe DONE ===');
  }
}

async function selfConnectThenClose(srv: socket.TCPSocketServer): Promise<void> {
  await probeSelfConnect();
  try {
    srv.close();
    log('D listener closed');
  } catch (e) {
    log(`D close FAILED ${describe(e)}`);
  }
}
