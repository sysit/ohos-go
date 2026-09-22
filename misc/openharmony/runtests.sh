#!/bin/bash
# runtests.sh cross-compiles Go packages for OpenHarmony and runs their tests on
# a device through the go_openharmony_<arch>_exec wrapper.
#
#	runtests.sh                  阶段 2 核心包（约 30 分钟）
#	runtests.sh io/fs text/template   只跑指定包
#	runtests.sh --all            B 层全量：std 里有测试的包
#	runtests.sh --list           只打印将要跑的清单，不跑
#
# 默认给 go test 加 -short。**这不是偏好，是设备物理上跑不动**：4GB guest 装不下
# 那些「按平台位数模拟海量条目」的测试。2026-09-21 实测 archive/zip 的
# TestZip64LargeDirectory 要 ~16GB 峰值 RSS，直接把 guest 打成 kernel panic
# （`Out of memory and no killable processes`）—— 注意后果不只是那个包 FAIL，
# **是模拟器卡死、整轮作废**。该测试自己写了 `testing.Short()` 跳过（Go 官方在受限
# 环境也是这个做法）。要跑完整模式加 --long，但请先确认 guest 内存够。
#
# 一个包一次 `go test`，而不是一把 `go test std`。理由是「可中断、可续跑、可
# 定位」：go test 自己的 -timeout 是**设备侧测试二进制**执行的，一旦 hdc 卡住，
# 主机侧没有任何东西会杀掉这次调用，整个后台任务会静默躺平。这里给每个包配
# 主机侧墙钟和独立日志，结论追加到 results.tsv，重跑自动跳过已 PASS。
#
# 环境：
#	OHOS_SDK	OpenHarmony SDK 根目录（默认 DevEco Studio 的那个）
#	OHOS_TARGET	hdc -t 目标（默认 127.0.0.1:5555，模拟器）
#
# 三个硬前提（照抄会踩，见 docs/ohos-full-test-plan.md §2）本脚本自己设好：
#   - 用仓库自带的 bin/go，不用 ~/go1.27.1-ohos（旧树，缺默认 PIE → Signal 11）
#   - CC 写 SDK clang 的绝对路径（裸 clang 会解析到 Apple clang）
#   - $GOROOT/bin 在 PATH 上（否则包装根本不被调用，退化成 exec format error）
#
# 所有结论都是「退出码 + 设备侧真实状态」双保险：设备没回传状态时包装返回 125，
# 不会伪装成 PASS。

set -u

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SRC="$REPO/src"
GOTOOL="$REPO/bin/go"

OHOS_SDK=${OHOS_SDK:-/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony}
OHOS_TARGET=${OHOS_TARGET:-127.0.0.1:5555}

OUT=ohos-test-results
HOST_TIMEOUT=300
DEV_TIMEOUT=240
VERBOSE=
SHORT=-short
FORCE=0
LIST_ONLY=0
ALL=0

die() { echo "runtests.sh: $*" >&2; exit 1; }

usage() {
	sed -n '2,26p' "$0" | sed 's/^#\{1\} \{0,1\}//'
	exit "${1:-0}"
}

while [ $# -gt 0 ]; do
	case $1 in
	-a|--all)   ALL=1 ;;
	--long)     SHORT= ;;   # 不加 -short：仅当 guest 内存足够时用
	-l|--list)  LIST_ONLY=1 ;;
	-f|--force) FORCE=1 ;;
	-v)         VERBOSE=-v ;;
	-o)         OUT=$2; shift ;;
	-t)         HOST_TIMEOUT=$2; shift ;;
	-T)         DEV_TIMEOUT=$2; shift ;;
	-h|--help)  usage 0 ;;
	--)         shift; break ;;
	-*)         die "unknown flag $1 (try --help)" ;;
	*)          break ;;   # 第一个非选项参数 = 包名，后面全是包名
	esac
	shift
done

[ -x "$GOTOOL" ] || die "$GOTOOL 不存在 —— 先按 CLAUDE.md 跑一次 ./make.bash"

# 旧工具链污染测量本轮已经中过一次（见 docs §9），所以每次都验一下再跑。
if [ "$SRC/internal/platform/supported.go" -nt "$GOTOOL" ]; then
	echo "warning: bin/go 比 src/internal/platform/supported.go 旧，可能不含最新的平台改动" >&2
fi

export GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1
export CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"
export PATH="$REPO/bin:$PATH"
export OHOS_TARGET

# 设备先探活。不探的话设备挂掉时整轮 262 个有测试的包全变 WRAPPER —— 分类是对的，但白刷一
# 轮、还把 results.tsv 灌满噪音（2026-09-21 真发生过：模拟器空转 474% CPU 不自愈）。
HDC=${HDC:-hdc}
if ! "$HDC" -t "$OHOS_TARGET" shell 'echo ok' 2>&1 | grep -q ok; then
	die "设备 ${OHOS_TARGET} 不可达 —— 先确认模拟器活着（hdc list targets），再跑"
fi

# 探活通过 ≠ 系统启动完成。**hdc 会抢答**：冷启动 15 秒就能 `echo ok`，但那时 guest
# 还在起系统服务，内存被开机高峰占着。此间跑测试会撞上去 —— 2026-09-21 实测
# `zip.test` 要 ~840MB，直接把 guest 打成 `Out of memory and no killable processes`
# → kernel panic，模拟器卡在 400% CPU 空转、只能重启。**整轮 215 个包白刷。**
#
# 判据只能用进程数收敛：`/proc/uptime` 被拒，`param get sys.boot_completed` 不存在。
# 实测冷启动 t+45s 有 210 个进程，t+185s 收敛到 194 并稳住。等它连着两次不比上次多。
echo "==> 等设备启动完成（进程数收敛）..."
prev=-1; stable=0
for _ in $(seq 1 40); do
	n=$("$HDC" -t "$OHOS_TARGET" shell 'ps -ef 2>/dev/null | wc -l' 2>/dev/null | tr -d '\r' | head -1)
	case $n in ''|*[!0-9]*) n=0 ;; esac
	if [ "$n" -gt 0 ] && [ "$n" -le "$prev" ]; then
		stable=$((stable+1))
		[ "$stable" -ge 2 ] && { echo "==> 就绪（${n} 个进程）"; break; }
	else
		stable=0
	fi
	prev=$n
	sleep 15
done

# 阶段 2 核心。docs §7 阶段 2 原本还把 internal/platform 和 cmd/go/internal/work
# 列进来，但那两个是 A 层（宿主测试，验的是编译器与平台表），交叉编译再送上设备
# 没有意义 —— §3 明确要求别把三层混在一起，它们归 ./all.bash。
CORE=(
	runtime os os/exec syscall
	net net/url net/http
	time mime testing
	strings io/fs text/template
)

if [ $# -gt 0 ]; then
	PKGS=("$@")
elif [ "$ALL" = 1 ]; then
	PKGS=()
	while IFS= read -r p; do
		[ -n "$p" ] && PKGS+=("$p")
	done < <("$GOTOOL" -C "$SRC" list \
		-f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' std)
else
	PKGS=("${CORE[@]}")
fi
[ ${#PKGS[@]} -gt 0 ] || die "包清单为空"

if [ "$LIST_ONLY" = 1 ]; then
	printf '%s\n' "${PKGS[@]}"
	exit 0
fi

mkdir -p "$OUT/logs"
RESULTS="$OUT/results.tsv"
[ -f "$RESULTS" ] || printf 'package\tstatus\tseconds\tskips\n' >"$RESULTS"
MARK="$OUT/.timed-out"

echo "==> 工具链 $("$GOTOOL" version)"
# 变量一律加花括号：bash 3.2（macOS 自带）会把紧跟 `$VAR` 的多字节字符当成变量名
# 的一部分，`$OHOS_TARGET，` 会报 "unbound variable"。这里全是中文，必踩。
echo "==> 目标 ${OHOS_TARGET}，${#PKGS[@]} 个包，主机墙钟 ${HOST_TIMEOUT}s（设备侧 -timeout ${DEV_TIMEOUT}s）"
echo "==> 结果 ${RESULTS}，日志 ${OUT}/logs/"

passed=0; failed=0; timedout=0; wrapped=0; skipped=0
FAILED_PKGS=()

# 让每个后台任务自成进程组：超时时必须连同它的子进程一起收掉。只杀 go 是不够的
# —— 包装会抱着 flock 变成孤儿，后面每个包都卡在 lock() 上。
set -m
for pkg in "${PKGS[@]}"; do
	if [ "$FORCE" = 0 ] && awk -F'\t' -v p="$pkg" \
		'$1==p && $2=="PASS"{found=1} END{exit !found}' "$RESULTS"; then
		skipped=$((skipped+1))
		printf '  %-30s (already passed)\n' "$pkg"
		continue
	fi

	log="$OUT/logs/$(echo "$pkg" | tr / _).log"
	rm -f "$MARK"
	start=$SECONDS

	# stdin 接 /dev/null：后台任务若去读终端会被 SIGTTIN 停住。
	"$GOTOOL" -C "$SRC" test -count=1 -timeout "${DEV_TIMEOUT}s" $SHORT $VERBOSE "$pkg" \
		>"$log" 2>&1 </dev/null &
	pid=$!
	# 看门狗的 fd 全部丢掉。否则里面的 `sleep` 会继承调用者的 stdout —— 收掉子
	# shell 并不杀它的孩子，那个 sleep 就把输出管道多攥 300 秒，外层看起来像卡死。
	(
		sleep "$HOST_TIMEOUT"
		kill -0 "$pid" 2>/dev/null || exit 0
		: >"$MARK"
		kill -TERM -"$pid" 2>/dev/null
		sleep 5
		kill -KILL -"$pid" 2>/dev/null
	) >/dev/null 2>&1 </dev/null &
	watchdog=$!
	# 2>/dev/null 吃掉 bash 的 "Terminated: 15" 作业通知：那是我们主动杀的，不该
	# 在后台日志里长得像个错误。
	wait "$pid" 2>/dev/null; rc=$?
	# 连看门狗的进程组一起收，顺带带走里面那个 sleep。
	kill -TERM -"$watchdog" 2>/dev/null || kill "$watchdog" 2>/dev/null
	wait "$watchdog" 2>/dev/null

	elapsed=$((SECONDS-start))
	if [ -e "$MARK" ]; then
		status=TIMEOUT; timedout=$((timedout+1))
	elif [ "$rc" = 0 ]; then
		status=PASS; passed=$((passed+1))
	elif [ "$rc" = 125 ] || grep -q '^go_openharmony_exec:' "$log"; then
		# 包装自己失败：设备没连上，或设备侧拒绝 exec。go test 会把子进程的 125
		# 压成 1，退出码认不出来，只能认包装自己的 stderr 前缀 —— 把这一类单独
		# 标出来很值：它和「测试跑了但挂了」是完全不同的排查方向。
		status=WRAPPER; wrapped=$((wrapped+1))
	else
		status=FAIL; failed=$((failed+1))
	fi

	# SKIP 只有 -v 才看得见。§8 要求 SKIP 数必须显式打印 —— 它突然变多就等于
	# 测试在悄悄失效。
	skips=-
	if [ -n "$VERBOSE" ]; then
		skips=$(grep -c -- '--- SKIP' "$log" 2>/dev/null || true)
		[ -n "$skips" ] || skips=0
	fi
	printf '%s\t%s\t%s\t%s\n' "$pkg" "$status" "$elapsed" "$skips" >>"$RESULTS"

	case $status in
	PASS)
		printf '  \033[32m%-30s %-8s %ss\033[0m\n' "$pkg" "$status" "$elapsed" ;;
	TIMEOUT)
		printf '  \033[35m%-30s %-8s %ss\033[0m\n' "$pkg" "$status" "$elapsed"
		FAILED_PKGS+=("$pkg") ;;
	*)
		printf '  \033[31m%-30s %-8s %ss  %s\033[0m\n' "$pkg" "$status" "$elapsed" "$log"
		FAILED_PKGS+=("$pkg") ;;
	esac
done

echo
echo "==> PASS ${passed}  FAIL ${failed}  TIMEOUT ${timedout}  WRAPPER ${wrapped}  （跳过已 PASS ${skipped}）"
if [ ${#FAILED_PKGS[@]} -gt 0 ]; then
	echo "==> 要看日志的包："
	printf '    %s\n' "${FAILED_PKGS[@]}"
fi
echo "==> 续跑：再跑一次本脚本即可，已 PASS 会跳过；要重跑加 --force"

[ "$failed" = 0 ] && [ "$timedout" = 0 ] && [ "$wrapped" = 0 ]
