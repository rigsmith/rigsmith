#!/usr/bin/env bash
# Functional round-trip smoke test for a josh-proxy binary. Hermetic: every
# byte of git traffic stays on 127.0.0.1 — upstream is a local bare repo
# served over real smart HTTP by scripts/smoke/git-cgi-server.
#
# Usage: josh-proxy-smoke.sh <path-to-josh-proxy> <scratch-dir>
# Run from the repo root (builds the CGI helper with `go build`).
#
# Mirrors what `rig stack` does with the engine (internal/rig/cli/stackjosh.go
# on feat/ws): spawn flags are the same --local/--remote/--port/--no-background
# shape, and the exercised operations map to the verbs:
#   1. filtered clone through :prefix        → rig stack init
#   2. pinned @sha fetch                     → rig stack pull
#   3. reverse push through the filter       → rig stack send (the critical path)
#   4. shutdown, restart, warm-cache refetch → cache survives a clean stop
# Boot readiness (TCP within 10s) and teardown (exit within 2s of SIGINT, no
# orphans) are asserted on both proxy runs.
set -euo pipefail

JOSH_BIN=$1
WORK=$2

say()  { printf '\n== %s\n' "$*"; }
die()  { printf 'SMOKE FAIL: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*) GOOS=windows ;;
*) GOOS=unix ;;
esac

# Native binaries (josh-proxy, git http-backend) need Windows-style paths;
# cygpath -m yields C:/forward/slash form, which both they and bash accept.
topath() {
	if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi
}

mkdir -p "$WORK"
WORK=$(cd "$WORK" && pwd)
JOSH_BIN=$(cd "$(dirname "$JOSH_BIN")" && pwd)/$(basename "$JOSH_BIN")
CACHE="$WORK/josh-cache"
REPOS="$WORK/repos"
mkdir -p "$CACHE" "$REPOS"

# Hermetic git identity; no reliance on runner-level git config.
export GIT_AUTHOR_NAME=smoke GIT_AUTHOR_EMAIL=smoke@rigsmith.test
export GIT_COMMITTER_NAME=smoke GIT_COMMITTER_EMAIL=smoke@rigsmith.test

GITSRV_PID= PROXY_PID=
cleanup() {
	[ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null || true
	[ -n "$GITSRV_PID" ] && kill "$GITSRV_PID" 2>/dev/null || true
}
trap cleanup EXIT

say "local upstream over smart HTTP"
EXE=""
[ "$GOOS" = windows ] && EXE=".exe"
go build -o "$WORK/git-cgi-server$EXE" ./scripts/smoke/git-cgi-server

git init -q -b main "$WORK/seed"
echo "hello" >"$WORK/seed/README.md"
git -C "$WORK/seed" add . && git -C "$WORK/seed" commit -qm "add README"
mkdir "$WORK/seed/src"
echo "lib v1" >"$WORK/seed/src/lib.txt"
git -C "$WORK/seed" add . && git -C "$WORK/seed" commit -qm "add lib source"
FIRST_SHA=$(git -C "$WORK/seed" rev-list --max-parents=0 HEAD)
TIP_SHA=$(git -C "$WORK/seed" rev-parse HEAD)

git clone -q --bare "$WORK/seed" "$REPOS/upstream.git"
git -C "$REPOS/upstream.git" config http.receivepack true # allow anonymous push
git -C "$REPOS/upstream.git" symbolic-ref HEAD refs/heads/main

"$WORK/git-cgi-server$EXE" -root "$(topath "$REPOS")" >"$WORK/gitserver.log" 2>&1 &
GITSRV_PID=$!
GIT_PORT=""
for _ in $(seq 1 50); do
	GIT_PORT=$(sed -n 's/^PORT=//p' "$WORK/gitserver.log")
	[ -n "$GIT_PORT" ] && break
	sleep 0.1
done
[ -n "$GIT_PORT" ] || die "git-cgi-server did not report a port; log: $(cat "$WORK/gitserver.log")"
JOSH_PORT=$("$WORK/git-cgi-server$EXE" -pick | sed -n 's/^PORT=//p')
[ -n "$JOSH_PORT" ] || die "could not pick a port for josh-proxy"
echo "git server on :$GIT_PORT, josh-proxy will take :$JOSH_PORT"

# Same flag shape as stackjosh.go's spawn.
start_proxy() { # $1 = logfile
	"$JOSH_BIN" --local "$(topath "$CACHE")" --remote "http://127.0.0.1:$GIT_PORT" \
		--port="$JOSH_PORT" --no-background >"$1" 2>&1 &
	PROXY_PID=$!
	for _ in $(seq 1 100); do # 10s
		if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$JOSH_PORT/"; then return 0; fi
		kill -0 "$PROXY_PID" 2>/dev/null || die "josh-proxy exited at boot; log: $(cat "$1")"
		sleep 0.1
	done
	die "josh-proxy not TCP-ready within 10s; log: $(cat "$1")"
}

stop_proxy() {
	local pid=$PROXY_PID
	PROXY_PID=""
	if [ "$GOOS" = windows ]; then
		taskkill //F //IM "$(basename "$JOSH_BIN")" >/dev/null 2>&1 || true
	else
		kill -INT "$pid" 2>/dev/null || true
	fi
	local i=0
	while kill -0 "$pid" 2>/dev/null; do
		i=$((i + 1))
		if [ "$i" -ge 20 ]; then # 2s
			kill -9 "$pid" 2>/dev/null || true
			die "josh-proxy still running 2s after shutdown signal"
		fi
		sleep 0.1
	done
	wait "$pid" 2>/dev/null || true
	if [ "$GOOS" = windows ]; then
		if tasklist 2>/dev/null | grep -qi "josh-proxy"; then die "orphan josh-proxy process after shutdown"; fi
	else
		if pgrep -x josh-proxy >/dev/null 2>&1; then die "orphan josh-proxy process after shutdown"; fi
	fi
}

say "boot josh-proxy"
start_proxy "$WORK/proxy1.log"

say "filtered clone (rig stack init)"
git clone -q "http://127.0.0.1:$JOSH_PORT/upstream.git%3Aprefix=lib.git" "$WORK/filtered"
[ -f "$WORK/filtered/lib/README.md" ] || die "lib/README.md missing from filtered clone"
[ -f "$WORK/filtered/lib/src/lib.txt" ] || die "lib/src/lib.txt missing from filtered clone"
[ "$(git -C "$WORK/filtered" rev-list --count HEAD)" = 2 ] || die "filtered clone should have exactly the 2 upstream commits"
git -C "$WORK/filtered" log --oneline | grep -q "add README" || die "'add README' missing from filtered log"
git -C "$WORK/filtered" log --oneline | grep -q "add lib source" || die "'add lib source' missing from filtered log"
git -C "$WORK/filtered" log --follow --format=%s -- lib/src/lib.txt | grep -qx "add lib source" ||
	die "--follow on lib/src/lib.txt did not reach the original commit message"

say "pinned-SHA fetch (rig stack pull)"
git init -q -b main "$WORK/pin"
git -C "$WORK/pin" fetch -q "http://127.0.0.1:$JOSH_PORT/upstream.git@${FIRST_SHA}%3Aprefix=lib.git"
PIN_TREE=$(git -C "$WORK/pin" ls-tree -r --name-only FETCH_HEAD)
echo "$PIN_TREE" | grep -qx "lib/README.md" || die "pinned fetch is missing lib/README.md"
if echo "$PIN_TREE" | grep -q "lib/src/lib.txt"; then
	die "pinned fetch at the first commit contains lib/src/lib.txt — @sha resolved to the branch tip, not the pinned commit"
fi

say "reverse push round-trip (rig stack send)"
echo "lib v2" >>"$WORK/filtered/lib/src/lib.txt"
git -C "$WORK/filtered" commit -aqm "roundtrip: update lib through the filter"
# josh refuses to create a ref that doesn't exist on the remote unless told
# which ref to root it on (`-o base=...`). Creating a fresh PR branch is the
# normal case for `rig stack send`, so its push must pass the same option —
# core/gitrepo's Push does not yet (caught by this suite; flagged on feat/ws).
git -C "$WORK/filtered" push -q -o base=refs/heads/main origin HEAD:refs/heads/roundtrip
git clone -q -b roundtrip "http://127.0.0.1:$GIT_PORT/upstream.git" "$WORK/direct" # no josh
grep -q "lib v2" "$WORK/direct/src/lib.txt" || die "pushed change not at src/lib.txt in upstream (prefix not stripped?)"
[ ! -e "$WORK/direct/lib" ] || die "upstream grew a lib/ directory — reverse filter did not strip the prefix"
[ "$(git -C "$WORK/direct" log -1 --format=%s)" = "roundtrip: update lib through the filter" ] ||
	die "commit message did not survive the reverse filter"
[ "$(git -C "$WORK/direct" rev-parse HEAD^)" = "$TIP_SHA" ] ||
	die "pushed commit is not parented on the upstream tip — ancestry was rewritten"

say "clean shutdown, then warm-cache refetch"
stop_proxy # asserts ≤2s exit + no orphans; a corrupt --local cache would surface next
start_proxy "$WORK/proxy2.log"
t0=$SECONDS
git clone -q "http://127.0.0.1:$JOSH_PORT/upstream.git%3Aprefix=lib.git" "$WORK/filtered2"
dt=$((SECONDS - t0))
[ -f "$WORK/filtered2/lib/src/lib.txt" ] || die "warm-cache clone is missing lib/src/lib.txt"
[ "$dt" -le 30 ] || die "warm-cache refetch took ${dt}s — cache was likely rebuilt from scratch"
echo "warm-cache refetch in ${dt}s"
stop_proxy

say "smoke OK: filtered clone, pinned fetch, reverse push, warm-cache refetch all passed"
