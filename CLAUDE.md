# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

A fork of the Go toolchain (`golang/go`) that adds **OpenHarmony (OHOS)** as a supported platform. It is a full Go source tree, not a Go module. `VERSION` tracks the upstream release it is based on (currently `go1.26.5`).

Branches:
- `release-branch.go1.26` — the active OHOS tree (upstream go1.26.5 + OHOS delta)
- `ohos-1.24-base` — the previous generation (upstream go1.24.5 + OHOS delta), kept as the source of the authoritative delta
- `main` — vintage OHOS tree with **no common history** with the other two; PRs from a dev branch into `main` need a synthetic merge commit (see the upgrade guide §8.2)

## Commands

Everything runs from `src/`. The repo ships a prebuilt host toolchain in `bin/` (`bin/go`, `bin/gofmt`) for the machine it was last built on.

```bash
# Rebuild the toolchain (3-stage bootstrap; requires Go >= 1.24.6 via GOROOT_BOOTSTRAP or PATH)
cd src && ./make.bash

# Rebuild + run the full Go test suite
cd src && ./all.bash

# Test a single package, or a single test
../bin/go test runtime
../bin/go test -run TestLink ./cmd/link/internal/ld
../bin/go test -v -run TestName ./internal/platform
```

**Fresh GOCACHE is mandatory after a toolchain change.** A stale cache makes reloc enum numbering drift and the link step fails with `unknown reloc to ...: 105 (RelocType(105))`. Run `go clean -cache` or point `GOCACHE` at a new directory.

If you change `src/cmd/internal/objabi/reloctype.go`, regenerate `reloctype_string.go` with `stringer` (the in-tree `stringer` isn't built until the toolchain is, so do it in a scratch module and copy the result back).

Cross-compiling for OHOS (clang is the default CC for this GOOS; external linking is forced):

```bash
OHOS_SDK=/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony
GOOS=openharmony GOARCH=arm64 CGO_ENABLED=1 \
CC="$OHOS_SDK/native/llvm/bin/clang --target=aarch64-linux-ohos --sysroot=$OHOS_SDK/native/sysroot -D__MUSL__" \
./bin/go build -buildmode=c-shared -o out.so .
```

`CC` **must name the SDK's clang by absolute path.** A bare `clang` resolves to Apple's
`/usr/bin/clang`, which does not know the `aarch64-linux-ohos` target and dies with
`clang: error: unable to execute command: posix_spawn failed: No such file or directory`.
Also export `OHOS_SDK` as above or define it inline — the snippet used to assume an unset
variable was already in the environment.

Only packages that need **external linking** invoke clang at all, so a wrong `CC` stays
invisible until some cgo-using package breaks. That selectivity is what makes this easy to
miss: it looks like "one weird package" rather than a bad toolchain setting.

Running cross-compiled tests on a device/emulator (`-exec` wrapper, device facts, and the
wrapper's known gaps around working directory and testdata) is covered in
`docs/ohos-full-test-plan.md`; the short version is `GOOS=openharmony GOARCH=arm64 \
CGO_ENABLED=1 ./bin/go test <pkg>`, with `$GOROOT/bin` on `PATH` so the
`go_openharmony_<arch>_exec` wrapper is found.

## Architecture: the OpenHarmony port

The port's defining trick is a **split identity**:

- Build tags and filenames use `openharmony` (`//go:build openharmony`, `*_openharmony.go`).
- But `runtime.GOOS == "linux"` — `src/internal/goos/zgoos_openharmony.go` sets `GOOS = "linux"` and `IsOpenharmony = 1`. Every "is this Linux?" check in the runtime and stdlib therefore stays true, and the port layers on top via `runtime.IsOpenharmony` (a const in `runtime/extern.go`).
- Supported targets are only `openharmony/amd64` and `openharmony/arm64`.

`zgoos_openharmony.go` is **generated** by `src/internal/goos/gengoos.go`; edit the generator, not the generated file.

The hard part of the port is **musl emulated TLS**, because OHOS c-shared libraries are loaded into a musl process where the Go runtime cannot use the standard TLS model:

- `runtime/os_linux.go` detects musl at init (`libpreinit`, `_cgo_is_musl`, `libmusl`) and captures the C `environ` pointer rather than reading `/proc/self/environ`.
- `runtime/runtime1.go` (`procEnviron`, `muslEnviron`, `goenvs_musl`, `readNullTerminatedStringsFromFile`) parses argv/env from those pre-captured C pointers. **This path must not allocate** — it runs before `mallocinit` (see pitfall below).
- `runtime/cgo/callbacks_musl_linux.go` + `gcc_musl_linux.c` + `runtime/tls_arm64.{h,s}` implement TLS descriptors and `__tls_get_addr` via `pthread_key` TSD.
- The compiler and linker emit/consume the extra relocations `R_AMD64_TLS_GD` / `R_ARM64_TLS_GD` (`cmd/internal/objabi/reloctype.go`, `cmd/internal/obj/x86/asm6.go`, `cmd/internal/obj/arm64/asm7.go`) and resolve the c-shared entry points in `runtime/rt0_openharmony_amd64.s` / `rt0_openharmony_arm64.s`.
- `cmd/link/internal/ld/decodesym.go`'s `decodetypeGcprogShlibByReloc` reads GC data out of shared-library type descriptors, which normal Go binaries never need.

Platform registration lives in `src/internal/platform/{zosarch,supported}.go`, `src/cmd/dist/build.go`, and `src/cmd/go/internal/{cfg,work}`. OHOS-specific stdlib replacements: `net/interface_table_openharmony.go` (getifaddrs-based), `time/zoneinfo_openharmony.go` (parses musl's packed `tzdata`), `mime/type_openharmony.go`.

### Pitfall: early-init allocation order

Upstream moved `getGodebugEarly()` to run *before* `mallocinit()`. The OHOS musl hunk originally read `/proc/self/environ` with `append`/`make`/`gostring`, which crashes with a nil dereference at `.so` load. The current implementation scans the C `environ` pointer captured by `libpreinit` and never allocates. Logged as commit `ce51b2141c`. **Any change to the early GODEBUG/environ path must stay allocation-free.**

## Upstream merges

`docs/go-upgrade-guide.md` is the authoritative, reusable playbook for merging a newer upstream Go tag into an OHOS branch. Read it before starting a merge. Its two load-bearing ideas:

1. **Extract the OHOS delta first** (`git diff <old-upstream-tag> <OHOS-HEAD>`) so conflicts can be resolved as "take theirs, then re-apply the OHOS hunk" rather than by guessing.
2. **Verify by diffing against the new upstream tag at the end** — the residual diff for each file must be *exactly* the OHOS delta, nothing more. Zero diff on a file means upstream has since implemented that change natively and the OHOS hunk should be dropped (the guide §3.4 lists ones already subsumed, e.g. `encoding/pem`, `net/url`, `os/exec/lp_*`).

Its §4 is the checklist of OHOS capabilities to re-verify after every merge, and §6 catalogues the specific conflicts previously hit (arm64 `asm7.go` case-number collisions, duplicate `sizeFixups` loops, a dropped `goos` var in `cmd/go/go_test.go`).

The `.claude/skills/merge-upstream/` skill is the executable summary of that playbook — it adds the conflict-classification table, the residual-diff verification gate, and the PR history-bridging `commit-tree` recipe.

### Git remote state

This clone is a **partial clone** (`blob:none` promisor) of the fork, and historically carried **no upstream Go tags at all** — only two fork tags (`v1.24.5`, `v1.26.5-beta1`). An `upstream` remote pointing at `https://github.com/golang/go.git` has since been added, but its tags are **not yet fetched**; `git fetch upstream --tags` (or `git fetch upstream tag go1.27.0`) is required before any upstream tag can be merged. The pre-flight section of the merge skill assumes this.

## Tooling notes

The ECC plugin's Go tooling assumes an *application* Go module and is miscalibrated for a toolchain source tree:

| ECC assumes | Reality here |
|---|---|
| `/go-build` → `go build ./...`, `golangci-lint`, `staticcheck` | The tree is built with `./make.bash`; the Go project does not use golangci-lint and running it produces overwhelming noise |
| `/go-test` → table-driven TDD, 80% coverage | Tests are `go test <pkg>` / `./all.bash`; `test/` has its own `test/run.go` harness |
| `go-build-resolver` agent | Unhelpful for reloc-enumeration drift and musl TLS link errors — the failures this port actually produces |

Prefer plain `./make.bash` plus a scoped `go test <single-package>` over invoking those commands. `go-reviewer` is still useful for the ordinary Go code in the tree.

There is no tooling for Go assembly (`.s` in plan9 syntax — `rt0_openharmony_*.s`, `tls_arm64.s`, `asm_arm64.s`) or for linker/relocation internals; both must be read by hand.
