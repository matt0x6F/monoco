#!/usr/bin/env bash
# spike.sh — Test release-time go.sum population strategies.
#
# Question: when monoco has just created local tag foo/v1.1.0 and rewritten
# bar/go.mod to require it (dropping the local replace), how do we get
# bar/go.sum populated so `go build` works?
#
# Strategies tested:
#   B) Filesystem GOPROXY serving a zip built from the tagged tree
#   C) Compute h1: hashes with golang.org/x/mod/sumdb/dirhash (no network)
#
# Strategy A (GOPROXY=direct against a bare git remote) was tried first and
# confirmed NOT WORKABLE without real DNS + HTTP meta-tag serving; Go's
# direct resolver requires reaching `https://<host>/<path>?go-get=1` to
# discover the VCS URL. git config insteadOf doesn't bypass that step.

set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d -t monoco-poc04)"
trap 'echo; echo "(tmpdir preserved: $work)"' EXIT

echo "=== setup ==="
echo "work: $work"

repo="$work/repo"
mkdir -p "$repo"
cd "$repo"
git init --quiet -b main
git config user.email p@o.c
git config user.name poc

mkdir -p foo bar
cat > foo/go.mod <<EOF
module example.local/mono/foo

go 1.22
EOF
cat > foo/foo.go <<'EOF'
package foo

func Hello() string { return "v1.0.0" }
EOF

cat > bar/go.mod <<EOF
module example.local/mono/bar

go 1.22

require example.local/mono/foo v1.0.0

replace example.local/mono/foo => ../foo
EOF
cat > bar/bar.go <<'EOF'
package main

import (
	"fmt"
	foo "example.local/mono/foo"
)

func main() { fmt.Println(foo.Hello()) }
EOF

cat > go.work <<EOF
go 1.22

use (
	./foo
	./bar
)
EOF

git add -A && git commit -q -m "init"
git tag foo/v1.0.0

echo "=== bump foo; create tag foo/v1.1.0 ==="
cat > foo/foo.go <<'EOF'
package foo

func Hello() string { return "v1.1.0" }
EOF
git add -A && git commit -q -m "bump foo"
git tag foo/v1.1.0

# Rewrite bar/go.mod: drop replace, bump require.
cat > bar/go.mod <<EOF
module example.local/mono/bar

go 1.22

require example.local/mono/foo v1.1.0
EOF
git add -A && git commit -q -m "release: foo v1.1.0"

# Per-module builds must not see go.work (would resolve foo locally).
export GOWORK=off

try_build() {
	local label="$1"; shift
	echo "--- $label ---"
	pushd "$repo/bar" >/dev/null
	rm -f go.sum
	export GOSUMDB=off
	if build_output=$("$@" 2>&1); then
		echo "BUILD: ok"
		echo "go.sum:"; cat go.sum 2>/dev/null || echo "(none)"
	else
		echo "BUILD: FAIL"
		echo "$build_output" | head -12 | sed 's/^/  /'
	fi
	popd >/dev/null
}

echo ""
echo "=== STRATEGY B: filesystem GOPROXY with pre-built module zip ==="
# Build proxy layout. We need: list, v1.1.0.info, v1.1.0.mod, v1.1.0.zip
proxy="$work/proxy"
modpath="example.local/mono/foo"
veri_dir="$proxy/$modpath/@v"
mkdir -p "$veri_dir"

# Use a small Go helper to produce the canonical module zip.
helper="$work/zipper"
mkdir -p "$helper"
cat > "$helper/go.mod" <<EOF
module zipper

go 1.22

require golang.org/x/mod v0.17.0
EOF
cat > "$helper/main.go" <<'EOF'
package main

import (
	"io"
	"os"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

// zipper <moddir> <module-path> <version> <out-zip>
// Prints:  h1:...\n  h1-go.mod:...
func main() {
	dir, mp, ver, out := os.Args[1], os.Args[2], os.Args[3], os.Args[4]

	f, err := os.Create(out)
	check(err)
	err = modzip.CreateFromDir(f, module.Version{Path: mp, Version: ver}, dir)
	check(err)
	check(f.Close())

	h1, err := dirhash.HashZip(out, dirhash.Hash1)
	check(err)

	h1mod, err := dirhash.Hash1([]string{"go.mod"}, func(name string) (io.ReadCloser, error) {
		return os.Open(dir + "/go.mod")
	})
	check(err)

	os.Stdout.WriteString(h1 + "\n")
	os.Stdout.WriteString(h1mod + "\n")
}

func check(err error) { if err != nil { println("ERR:", err.Error()); os.Exit(1) } }
EOF
cd "$helper"
go mod tidy 2>&1 | tail -2
go build -o zipper .

# Extract foo@v1.1.0 into a clean dir (as if cloned from a tagged release).
foodir="$work/foo-at-v1.1.0"
mkdir -p "$foodir"
cd "$repo"
git archive foo/v1.1.0 -- foo | tar -x -C "$foodir"
# archive places under ./foo/
hashes=$("$helper/zipper" "$foodir/foo" "$modpath" "v1.1.0" "$veri_dir/v1.1.0.zip")
h1=$(echo "$hashes" | sed -n 1p)
h1mod=$(echo "$hashes" | sed -n 2p)
echo "zip h1:     $h1"
echo "go.mod h1:  $h1mod"

# .info + .mod
cat > "$veri_dir/v1.1.0.info" <<EOF
{"Version":"v1.1.0","Time":"2026-01-01T00:00:00Z"}
EOF
cp "$foodir/foo/go.mod" "$veri_dir/v1.1.0.mod"
echo "v1.1.0" > "$veri_dir/list"

export GOPROXY="file://$proxy"
export GOFLAGS=-mod=mod
try_build "B (filesystem GOPROXY)" go build ./...

echo ""
echo "=== STRATEGY C: precomputed go.sum + GOPROXY=off ==="
# go.sum format:  <module> <version> <h1>\n<module> <version>/go.mod <h1mod>
sumfile="$repo/bar/go.sum.precomputed"
cat > "$sumfile" <<EOF
$modpath v1.1.0 $h1
$modpath v1.1.0/go.mod $h1mod
EOF

# With GOPROXY=off and no module cache entry, `go build` will refuse to
# download. Strategy C is only useful if something ELSE populated the cache
# (e.g. a prior workspace build). That's not the release flow, so expect FAIL.
run_c() {
	cp "$sumfile" "$repo/bar/go.sum"
	export GOPROXY=off
	go build ./...
}
try_build "C1 (go.sum only, GOPROXY=off — expected to fail)" bash -c "$(declare -f run_c); run_c"

# Strategy C makes sense ALONGSIDE Strategy B: we pre-seed go.sum so
# `go build` doesn't contact the proxy for hash verification at all.
echo ""
echo "--- C2 (precomputed go.sum + filesystem GOPROXY) ---"
cp "$sumfile" "$repo/bar/go.sum"
export GOPROXY="file://$proxy"
pushd "$repo/bar" >/dev/null
if go build ./... 2>&1; then
	echo "BUILD: ok (go.sum was honored, proxy served the zip)"
	echo "go.sum unchanged? $(diff -q go.sum "$sumfile" >/dev/null && echo yes || echo NO)"
else
	echo "BUILD: FAIL"
fi
popd >/dev/null

echo ""
echo "=== DONE ==="
