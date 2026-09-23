#!/bin/bash
# runtests.sh cross-compiles Go packages for OpenHarmony and runs their tests on
# a device through the go_openharmony_<arch>_exec wrapper.
#
#	runtests.sh                  阶段 2 核心包（约 30 分钟）
#	runtests.sh io/fs text/template   只跑指定包
#	runtests.sh --all            B 层全量：std 里有测试的包
#	runtests.sh --list           只打印将要跑的清单，不跑
#	runtests.sh --toolchain DIR  换一棵树跑（验收交付物：解包出来的 tar）
#	runtests.sh -o DIR           换结果目录（默认 ohos-test-results）
#	runtests.sh --selftest       只验「跳过判据」，不碰设备
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
# 结论按**树指纹**记账。results.tsv 是追加写的、重跑跳过已 PASS，所以在一棵改过的
# 树上重跑时，它会把上一轮的 PASS 当本轮结论用掉 —— 看着全绿，但那个「测过了」说的
# 是另一棵树（docs/ohos-release-roadmap.md ⑩）。现在每行都带整棵 bin/ 的 sha256，
# 指纹不同就不复用（旧格式的行没有指纹，一律重跑）。**发版前必须在交付物上跑一遍**：
#	runtests.sh --all --toolchain <解包出来的那棵树> -o ohos-test-results/release-tree
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
ROOT=$REPO          # 被测树。--toolchain 把它指向解包出来的交付物
EXTERNAL=0
SELFTEST=0

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
	# 别按行号截：头部注释一改就悄悄截错。打到 `set -u` 前一行为止。
	sed -n '2,/^set -u$/p' "$0" | sed '$d' | sed 's/^#\{1\} \{0,1\}//'
	exit "${1:-0}"
}

# 取 bin/ 下每个文件的 sha256 当树指纹，而不是 git HEAD：交付物是一棵**解包出来的 tar**，
# 里面没有 .git。而 bin/ 就是「哪把编译器编的这些测试二进制」的答案，
# 也正是 soak 文档要设备侧填的同一个数 —— 全项目只用一个指纹，免得两边对不上。
#
# 覆盖整个 bin/ 而不是只取 bin/go（2026-09-23 改）：那天实测到两棵树 bin/go 逐字节相同
# （sha256 都是 9cfddd97...）而 bin/go_openharmony_arm64_exec 不同 —— 一个嵌着构建机的
# 绝对路径、一个没有。只认 bin/go 会把这两棵树认成同一棵，于是「A 树跑绿」的结论被 B 树
# 静默复用，正是这个指纹存在的目的。列名仍叫 toolchain，值已改成整棵 bin/ 的哈希。
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

sha256_stdin() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum | cut -d' ' -f1
	else
		shasum -a 256 | cut -d' ' -f1
	fi
}

# 带相对路径一起哈希，这样「同名不同内容」和「多一个/少一个文件」都算换树。
#
# **只取 bin/ 顶层的文件（-maxdepth 1），子目录不算** —— 这不是随手写的：`bin/openharmony_arm64/`
# 是包装按需编出来的**目标架构工具链缓存**（见 guide §6.8），第一次跑设备测试时它会自己长出来。
# 把子目录算进去的话指纹会在**跑的过程中**变，于是每次断点续跑都算出新指纹、把上一轮的
# PASS 全部作废、从零重跑 —— 正好把这个脚本存在的理由（可中断可续跑）废掉。
# 顶层文件恰好就是会发出去的那几个（go、gofmt、两个 _exec 包装），也就是该被钉住的东西。
tree_fingerprint() {
	find "$ROOT/bin" -maxdepth 1 -type f | LC_ALL=C sort | while read -r f; do
		printf '%s %s\n' "${f#"$ROOT"/}" "$(sha256_of "$f")"
	done | sha256_stdin
}

# 「这行结论算不算本轮已 PASS」——跳过判据**只此一处**，主循环和 --selftest 共用。
# 各写一份迟早在列号上漂移，而漂移的后果正是这个脚本要修的毛病：看着全绿。
already_passed() {
	awk -F'\t' -v p="$1" -v fp="$FP" \
		'$1==p && $2=="PASS" && $5==fp{found=1} END{exit !found}' "$RESULTS"
}

# 判据的自检。不带 --selftest 就是死代码，所以顺带把它变成「换设备前先跑这个」的入口。
selftest() {
	tmp=$(mktemp -d) || exit 1
	FP=thisfingeprint
	RESULTS="$tmp/r.tsv"
	printf 'package\tstatus\tseconds\tskips\ttoolchain\n' >"$RESULTS"
	printf 'a\tPASS\t1\t-\t%s\n' "$FP"      >>"$RESULTS"   # 本轮已 PASS   → 跳过
	printf 'b\tPASS\t1\t-\tdeadbeef\n'      >>"$RESULTS"   # 别的树的 PASS → 不跳过
	printf 'c\tPASS\t1\t-\n'                >>"$RESULTS"   # 旧格式无指纹  → 不跳过
	printf 'd\tFAIL\t1\t-\t%s\n' "$FP"      >>"$RESULTS"   # 本轮但是 FAIL → 不跳过
	printf 'e\tPASS\t1\t-\t%s\n' "$FP"      >>"$RESULTS"   # 本轮已 PASS   → 跳过
	rc=0
	for c in a:yes b:no c:no d:no e:yes zz:no; do
		p=${c%%:*}; want=${c##*:}
		if already_passed "$p"; then got=yes; else got=no; fi
		if [ "$got" != "$want" ]; then
			echo "selftest: $p 期望 $want，实际 $got" >&2
			rc=1
		fi
	done
	# 指纹必须覆盖整棵 bin/，不能只认 bin/go：2026-09-23 实测到两棵树 bin/go 逐字节相同、
	# 而 go_openharmony_arm64_exec 不同（一个嵌着构建机绝对路径），只认 bin/go 就会把
	# 「在 A 树跑绿」的结论用到 B 树上。下面第三例就是那天那个形状。
	mkdir -p "$tmp/root/bin"
	ROOT="$tmp/root"
	printf a >"$ROOT/bin/go"
	printf b >"$ROOT/bin/go_openharmony_arm64_exec"
	fp1=$(tree_fingerprint)
	[ "$fp1" = "$(tree_fingerprint)" ] || { echo "selftest: 同一棵树两次指纹不同" >&2; rc=1; }
	printf bb >"$ROOT/bin/go_openharmony_arm64_exec"
	fp2=$(tree_fingerprint)
	[ "$fp1" != "$fp2" ] || { echo "selftest: 只改了 go_*_exec 指纹却没变 —— 指纹没覆盖整棵 bin/" >&2; rc=1; }
	printf c >"$ROOT/bin/gofmt"
	[ "$fp2" != "$(tree_fingerprint)" ] || { echo "selftest: 多一个文件指纹却没变" >&2; rc=1; }
	# 包装跑一次设备测试就会在 bin/ 下长出目标工具链缓存；指纹必须对此免疫，
	# 否则断点续跑的每一轮都算出新指纹、把上一轮的 PASS 全作废。
	fp3=$(tree_fingerprint)
	mkdir -p "$ROOT/bin/openharmony_arm64" && printf d >"$ROOT/bin/openharmony_arm64/compile"
	[ "$fp3" = "$(tree_fingerprint)" ] || { echo "selftest: bin/ 子目录（目标工具链缓存）影响了指纹 —— 续跑会失效" >&2; rc=1; }

	rm -rf "$tmp"
	[ "$rc" = 0 ] && echo "selftest: ok（6 例判据 + 4 例指纹）"
	return "$rc"
}

while [ $# -gt 0 ]; do
	case $1 in
	-a|--all)   ALL=1 ;;
	--long)     SHORT= ;;   # 不加 -short：仅当 guest 内存足够时用
	-l|--list)  LIST_ONLY=1 ;;
	-f|--force) FORCE=1 ;;
	--toolchain) ROOT=$2; EXTERNAL=1; shift ;;
	--selftest) SELFTEST=1 ;;
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

SRC="$ROOT/src"
GOTOOL="$ROOT/bin/go"

[ -x "$GOTOOL" ] || die "$GOTOOL 不存在 —— 先按 CLAUDE.md 跑一次 ./make.bash"
[ -d "$SRC" ] || die "$SRC 不存在 —— ${ROOT} 不像一棵 Go 树"

if [ "$SELFTEST" = 1 ]; then
	selftest
	exit $?
fi

# 旧工具链污染测量本轮已经中过一次（见 docs §9），所以每次都验一下再跑。
# 交付物里的 mtime 是冻结时刻的相对关系，这条判据在那儿没有意义（会假报）。
if [ "$EXTERNAL" = 0 ] && [ "$SRC/internal/platform/supported.go" -nt "$GOTOOL" ]; then
	echo "warning: bin/go 比 src/internal/platform/supported.go 旧，可能不含最新的平台改动" >&2
fi

export GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1
export CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__"
export PATH="$ROOT/bin:$PATH"
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
[ -f "$RESULTS" ] || printf 'package\tstatus\tseconds\tskips\ttoolchain\n' >"$RESULTS"
MARK="$OUT/.timed-out"

FP=$(tree_fingerprint)
if ! head -1 "$RESULTS" | grep -q toolchain; then
	echo "==> 注意：${RESULTS} 是旧格式（无 toolchain 列），里面的结论来源不明，本轮全部重跑"
fi

echo "==> 工具链 $("$GOTOOL" version)"
echo "==> 被测树 ${ROOT}$([ "$EXTERNAL" = 1 ] && echo "（交付物，非本仓库）")"
echo "==> 指纹 ${FP:0:12}（整棵 bin/ 的 sha256）—— 结果按它记账，换树即失效"
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
	if [ "$FORCE" = 0 ] && already_passed "$pkg"; then
		skipped=$((skipped+1))
		printf '  %-30s (already passed on this tree)\n' "$pkg"
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
	printf '%s\t%s\t%s\t%s\t%s\n' "$pkg" "$status" "$elapsed" "$skips" "$FP" >>"$RESULTS"

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
echo "==> 本轮树指纹 ${FP:0:12}（${ROOT}）"
echo "==> 续跑：再跑一次本脚本即可，同一棵树上的 PASS 会跳过；要重跑加 --force"

[ "$failed" = 0 ] && [ "$timedout" = 0 ] && [ "$wrapped" = 0 ]
