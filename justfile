# conformist justfile. Conventions: eng-design_patterns-justfile(7),
# eng-versioning(7). `default` runs the full local CI lane.

default: validate build test verify lint

# --- validate (cheap pre-build gate) ---

validate: validate-devshell

# The devshell must evaluate and build before anything else is worth trying.
#
# build the devShell to prove it evaluates
validate-devshell:
    nix build --no-link .#devShells.{{ arch() }}-linux.default

# --- lint ---

lint: lint-fmt lint-worktree lint-go

# Read-only gate via the self-consumed conformist `checks.formatting` derivation
# (a `conformist check` run; the read-only counterpart to the writing `nix fmt`).
#
# run the read-only formatting check
lint-fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.formatting" --no-link --print-build-logs

# Non-sandbox lane: run the IMPURE git-state whole-tree checks (e.g. git-remotes)
# against the WORKING TREE, where .git is available. These can't run in the
# sandboxed checks.formatting. Builds the impure config + binary via nix.
#
# run the impure git-state checks against the working tree
lint-worktree:
    #!/usr/bin/env bash
    set -euo pipefail
    cfg=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
    nix run '.#conformist' -- check --config-file "$cfg" --tree-root .

# Run golangci-lint (stock set per .golangci.yaml: default:all minus the curated
# disable list, plus dewey's analyzers — conformist#10/#22) via the purse-first
# custom build. golangci-lint loads packages with the devShell go, so the binary
# runs inside `nix develop`. Pin the golangci-lint cache to the worktree being
# linted ($PWD), ignoring any inherited GOLANGCI_LINT_CACHE: golangci-lint
# replays per-package diagnostics whose embedded absolute paths point at
# whichever worktree populated the cache, so a cache shared across worktrees —
# e.g. the merge hook's throwaway .merge-* worktree inheriting the session's
# sweatfile-pinned $WORKTREE/.tmp path — makes nolint/generated-file suppression
# fail open and leaks spurious findings, a non-deterministic merge gate
# (conformist#34). Per-$PWD isolation keeps each worktree's cache self-consistent.
#
# run golangci-lint over the Go sources
lint-go:
    #!/usr/bin/env bash
    set -euo pipefail
    export GOLANGCI_LINT_CACHE="$PWD/.tmp/golangci-lint"
    bin=$(nix build --no-link --print-out-paths '.#golangci-lint-dewey')/bin/golangci-lint-dewey
    nix develop --command "$bin" run ./...

# --- build ---

build: build-gomod2nix build-go build-nix

# Run after changing deps.
#
# regenerate gomod2nix.toml from go.mod/go.sum
build-gomod2nix:
    nix develop --command gomod2nix

# OPT-IN: regenerate godyn-graph.json, the Go source dependency graph that drives
# the opt-in native (godyn) build backend (buildGoAuto, igloo#29;
# `.#conformist-native`). bga is the default backend now, so this is only needed
# when working on the godyn path — hence the debug group, not the `build`
# pipeline lane. CGO off — conformist is pure-Go — for clean file selection;
# captures the //go:embed patterns (e.g. cmd/conform/scaffold/*,
# cmd/init/init.toml). MUST run on x86_64-linux: the graph embeds linux/amd64-only
# sources, so regenerating on another host corrupts it (igloo#33). Run after
# changing imports/deps/embeds, then commit; drift-checked by debug-godyn-graph-drift.
#
# regenerate godyn-graph.json for the opt-in godyn build backend
[group("debug")]
debug-godyn-graph:
    nix develop --command env CGO_ENABLED=0 godyn-gen . godyn-graph.json

# Out-of-nix go build for a fast inner loop. Version/commit stay dev/unknown
# here; the nix build injects the real values (eng-versioning(7)).
#
# build the conformist binary out-of-nix for a fast inner loop
build-go: build-gomod2nix
    nix develop --command go build -o build/conformist .

# full nix build of the default package (injects the real version/commit)
build-nix:
    nix build --show-trace

# pass-through: `nix run` the conformist flake with ARGS
run-nix *ARGS:
    nix run . -- {{ ARGS }}

# Build conformist's own store-pinned pre-commit hook (build.preCommit, #47) and
# run it against the currently-staged files, exactly as a sweatfile
# `pre-commit = "conformist-pre-commit"` would. Verifies the new module output
# end-to-end (`conformist --staged --exit-zero-on-fix` with the store config);
# stage some files first. Manual dogfood loop, not in any aggregate / the CI lane.
#
# run the store-pinned pre-commit hook against the staged files
[group("explore")]
explore-pre-commit:
    #!/usr/bin/env bash
    set -euo pipefail
    hook=$(nix build --no-link --print-out-paths '.#conformist-pre-commit')
    "$hook/bin/conformist-pre-commit"

# Build conformist's own generated conformist.toml via self.lib.evalModule and
# cat it, to inspect the emitted [formatter.*] / [linter.*] stanzas. Verifies the
# Nix module's config generation (issue #4) without a full check run.
#
# print conformist's own generated conformist.toml
[group("explore")]
explore-show-config:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(nix build --no-link --print-out-paths --impure --expr \
      'let f = builtins.getFlake (toString ./.); s = builtins.currentSystem; p = import f.inputs.igloo { system = s; }; in (f.lib.evalModule p { imports = [ ./nix/conformist.nix ]; package = f.packages.${s}.conformist; }).config.build.configFile')
    cat "$out"

# Smoke-test the eng template end-to-end: instantiate it into a temp dir, lock +
# commit it, and run the sandboxed formatting check — the adopter's `nix flake
# init -t .#eng` path (templates/eng/, #17). Fetches conformist from github, so
# needs network; template-maintenance dev-loop, not in any aggregate / the CI lane.
#
# smoke-test the eng flake template end-to-end
[group("explore")]
explore-template-eng:
    #!/usr/bin/env bash
    set -euo pipefail
    src="{{ justfile_directory() }}"
    d=$(mktemp -d)
    trap 'rm -rf "$d"' EXIT
    cd "$d"
    nix flake init -t "$src#eng"
    git init -q
    git add -A
    nix flake lock
    git add flake.lock
    sys=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${sys}.formatting" --no-link --print-build-logs
    echo "explore-template-eng: template instantiates and passes checks.formatting"

# Behavioral fixtures for the built-in clippy linter (#69): build + run the
# clippy check/repair against a tiny offline Rust crate, asserting the check
# fails on a lint, passes when clean, and the repair --fix removes it. Pulls a
# Rust toolchain, so it is kept OUT of the verify/CI lane and built on demand
# here. See nix/linter-fixtures.nix (the clippy block).
#
# run the clippy linter behavioral fixtures
[group("explore")]
explore-clippy-fixture:
    #!/usr/bin/env bash
    set -euo pipefail
    sys=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${sys}.clippy-fixtures" --no-link --print-build-logs
    echo "explore-clippy-fixture: clippy check/repair fixtures pass"

# --- debug ---

# Auto-fix the golangci-lint findings that support --fix — notably tagalign
# struct-tag alignment, which gofumpt does NOT do (gofmt aligns the outer tag
# column but not the columns WITHIN a tag), so a new/renamed struct-tag key that
# becomes the longest shifts the toml column and only golangci's fixer knows the
# target. Same golangci-lint-dewey build and cache isolation as lint-go; run it,
# then re-check with `just lint-go`. Diagnostic aid for lint-go failures, not in
# any aggregate / the CI lane.
#
# auto-fix the golangci-lint findings that support --fix
[group("debug")]
debug-golangci-autofix:
    #!/usr/bin/env bash
    set -euo pipefail
    export GOLANGCI_LINT_CACHE="$PWD/.tmp/golangci-lint"
    bin=$(nix build --no-link --print-out-paths '.#golangci-lint-dewey')/bin/golangci-lint-dewey
    nix develop --command "$bin" run --fix ./...

# Build-backend microbench: godyn (native, per-package CA) vs buildGoApplication
# (bga) across four edit-locality phases, emitting wall-clock build durations to
# stats-me (stats-me-clients(1)) as |ms timers named
# gobuild.conformist.<backend>.<phase>. This name scheme is a protocol shared
# with igloo's dewey bench so numbers are directly comparable (igloo#28/#29).
# Uses igloo's nixgc (nixgc.1) to force genuinely cold rebuilds. EXPECTATION:
# cold favors bga (one `go build`, no per-package overhead); godyn wins the
# leaf/found incremental edits (recompiles only the changed dependency cone).
# Diagnostic only — not wired into any aggregate / the CI lane.
#
# benchmark the godyn and bga build backends
[group("debug")]
debug-bench-backends iterations="3":
    #!/usr/bin/env bash
    set -euo pipefail

    iters={{ iterations }}
    # just params are positional (`just debug-bench-backends 5`), so a stray
    # `iterations=5` would arrive as a literal string and silently empty the
    # `seq` loops — fail loudly instead.
    case "$iters" in
        '' | *[!0-9]*)
            echo "debug-bench-backends: iterations must be a positive integer, e.g. 'just debug-bench-backends 5' (got '$iters')" >&2
            exit 1
            ;;
    esac
    native_target=".#conformist-native"
    bga_target=".#conformist-native.passthru.bga"
    leaf_file="cmd/init/init.go"   # 1 dependent (init->cmd->main): small cone
    found_file="config/config.go"  # 4 dependents: large transitive cone

    host="${STATSD_HOST:-127.0.0.1}"; [ -n "$host" ] || host="127.0.0.1"
    port="${STATSD_PORT:-8125}"
    results="$(mktemp)"
    # Always drop the temp file and undo any in-flight edit, even on interrupt.
    trap 'rm -f "$results"; git checkout -- "$leaf_file" "$found_file" 2>/dev/null || true' EXIT

    # The edit phases revert via `git checkout`, so refuse to clobber real work.
    for f in "$leaf_file" "$found_file"; do
        if [ -n "$(git status --porcelain -- "$f")" ]; then
            echo "debug-bench-backends: $f is dirty; commit or stash it before benching" >&2
            exit 1
        fi
    done

    # statsd timing packet (stats-me-clients(1)); fire-and-forget UDP, no nc dep.
    statsd() { echo "$1:$2|ms" > "/dev/udp/$host/$port" 2>/dev/null || true; }

    echo "resolving nixgc from the locked igloo input..."
    nixgc="$(nix build --no-link --print-out-paths --impure --expr \
      'let f = builtins.getFlake (toString ./.); s = builtins.currentSystem; p = import f.inputs.igloo { system = s; }; in p.nixgc')/bin/nixgc"

    # Time one `nix build <target> --no-link`; echo elapsed milliseconds.
    timed_build() {
        local target="$1" t0 t1
        t0=$(date +%s%N)
        nix build "$target" --no-link >/dev/null 2>&1 \
            || { echo "debug-bench-backends: build failed for $target" >&2; exit 1; }
        t1=$(date +%s%N)
        echo $(( (t1 - t0) / 1000000 ))
    }

    # One cold sample for a backend: capture the live output, nixgc-reap it, time
    # the from-scratch rebuild. godyn outputs are content-addressed (referenced by
    # sibling .drvs) -> --with-referrers + seed the .drv; bga is one input-addressed
    # derivation -> seed the output alone. If the reap frees nothing, a live GC root
    # still anchors the closure (so the "rebuild" is a warm cache hit, not cold) —
    # skip the sample instead of emitting a bogus fast "cold" number. See the
    # result/keep-derivations note above the cold lane.
    cold_sample() {  # backend target with_referrers(yes|no)
        local backend="$1" target="$2" with_ref="$3" out drv reap_out
        out=$(nix build "$target" --no-link --print-out-paths)
        # The build we just ran holds a temporary GC root
        # (/nix/var/nix/temproots/<pid>) on its outputs. It stays LIVE until the
        # daemon/client releases it, and a live temproot cannot be swept — so first
        # `sleep` to let it go stale, then force a root enumeration (which deletes
        # stale temproot files) so nixgc's alive-set doesn't count it and refuse the
        # reap (igloo#28). A *concurrent* build's live temproot still can't be
        # cleared — the reap-freed-nothing guard below keeps that case honest.
        sleep 2
        nix-store -q --roots "$out" >/dev/null 2>&1 || true
        if [ "$with_ref" = yes ]; then
            drv=$(nix-store -q --deriver "$out")
            reap_out=$("$nixgc" reap --with-referrers "$out" "$drv" 2>&1) || true
        else
            reap_out=$("$nixgc" reap "$out" 2>&1) || true
        fi
        echo "$reap_out"
        if echo "$reap_out" | grep -qE 'reaped 0 path|nothing to reap'; then
            echo "  $backend cold: SKIPPED — reap freed nothing; a live GC root still anchors the closure (rebuild would be a cache hit, not cold)" >&2
            return 0
        fi
        record "$backend" cold "$(timed_build "$target")"
    }

    # Append a unique comment to a file (a real byte change — nix is
    # content-addressed, so mtime alone won't invalidate), time the rebuild,
    # revert. Echoes ms.
    edit_build() {
        local file="$1" target="$2" ms
        printf '\n// debug-bench-backends %s\n' "$(date +%s%N)" >> "$file"
        ms=$(timed_build "$target")
        git checkout -- "$file"
        echo "$ms"
    }

    record() {  # backend phase ms
        echo "$1 $2 $3" >> "$results"
        statsd "gobuild.conformist.$1.$2" "$3"
        printf '  %-7s %-6s %7s ms\n' "$1" "$2" "$3"
    }

    echo "warm-building both backends (baseline)..."
    nix build "$native_target" --no-link >/dev/null 2>&1
    nix build "$bga_target" --no-link >/dev/null 2>&1

    # nixgc only cold-nukes paths that no live GC root anchors. A stale `result`
    # symlink (e.g. from a prior `just build-nix`) roots a conformist closure, and
    # with the system's keep-derivations=true that transitively keeps the binary's
    # link .drv — and its per-package CA compile inputSrcs — alive, defeating the
    # reap. Remove it; the bench builds with --no-link so it never recreates one.
    # On a contended store the reap can still be refused by live temproots held by
    # other in-flight nix builds — nixgc respects those by design; the cold_sample
    # guard then skips honestly. Making cold robust on a busy host: conformist#21.
    if [ -L result ]; then rm -f result; fi

    echo "=== cold: full rebuild after nixgc reap ==="
    for _ in $(seq 1 "$iters"); do
        cold_sample native "$native_target" yes
        cold_sample bga    "$bga_target"    no
    done

    echo "=== warm: no-op rebuild ==="
    for _ in $(seq 1 "$iters"); do
        record native warm "$(timed_build "$native_target")"
        record bga    warm "$(timed_build "$bga_target")"
    done

    echo "=== leaf: edit $leaf_file (small cone) then rebuild ==="
    for _ in $(seq 1 "$iters"); do
        record native leaf "$(edit_build "$leaf_file" "$native_target")"
        record bga    leaf "$(edit_build "$leaf_file" "$bga_target")"
    done

    echo "=== found: edit $found_file (large cone) then rebuild ==="
    for _ in $(seq 1 "$iters"); do
        record native found "$(edit_build "$found_file" "$native_target")"
        record bga    found "$(edit_build "$found_file" "$bga_target")"
    done

    echo
    echo "=== summary  ms: min / median / mean / max  over $iters iter(s) ==="
    echo "emitted to stats-me as gobuild.conformist.<backend>.<phase> (query: stats-me-query)"
    echo "note: cold favors bga; godyn (native) wins the leaf/found incremental edits"
    for b in native bga; do
        for ph in cold warm leaf found; do
            awk -v b="$b" -v p="$ph" '$1==b && $2==p {print $3}' "$results" | sort -n | awk -v b="$b" -v p="$ph" '
                {a[NR]=$1; s+=$1}
                END{
                    if (NR>0) {
                        n=NR; if (n%2) med=a[(n+1)/2]; else med=(a[n/2]+a[n/2+1])/2;
                        printf "  %-7s %-6s %7d / %7.0f / %7.0f / %7d\n", b, p, a[1], med, s/n, a[n]
                    }
                }'
        done
    done

# Bisect WHY a given flake.nix fails flakeparse's shape match. Truncates the
# per-system `let` block one top-level binding at a time (from the end) and
# re-tests, so the first binding whose removal makes the file parse is the
# culprit. Uses flakeclobber as the parse oracle (its "not the recognized
# eachDefaultSystem shape" message IS a ParseFlake failure). For chasing an
# unexplained refusal in the fleet corpus — an unexplained refusal in a tool
# that rewrites flake.nix is worth understanding before a sweep. Diagnostic
# only; read-only with respect to the target.
#
# bisect which let binding makes a flake.nix fail the shape match
[group("debug")]
debug-flakeparse-bisect target:
    #!/usr/bin/env bash
    set -uo pipefail
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    bin="$tmp/flakeclobber"
    nix develop --command go build -o "$bin" ./cmd/flakeclobber || exit 2

    # Capture first, then grep. A `cmd | grep -q` pipeline is WRONG here: under
    # `set -o pipefail` the pipeline inherits flakeclobber's own exit 1, which
    # masks grep's success and makes the oracle report "parses" in exactly the
    # failing case.
    parses() {
        local out
        out=$("$bin" --old zzz --new "" "$1" 2>&1)
        case "$out" in
            *"not the recognized"*) return 1 ;;
            *) return 0 ;;
        esac
    }

    cp "{{ target }}" "$tmp/full.nix"
    if parses "$tmp/full.nix"; then
        echo "debug-flakeparse-bisect: {{ target }} ALREADY parses — nothing to bisect"
        exit 0
    fi
    echo "baseline: {{ target }} does NOT parse"

    # Top-level bindings of the per-system let block are the lines at the let
    # body's indent that look like `name =`. Collect their line numbers.
    mapfile -t binds < <(grep -nE '^        [a-zA-Z_][a-zA-Z0-9_'"'"'-]*[[:space:]]*=' "$tmp/full.nix" | cut -d: -f1)
    echo "found ${#binds[@]} candidate top-level let bindings"
    if [ "${#binds[@]}" -eq 0 ]; then
        echo "no candidates at the assumed indent — inspect by hand" >&2
        exit 1
    fi

    # For each binding, delete just that binding (up to the line before the
    # next one) and see whether the file starts parsing.
    # The LAST binding runs to the line before `in`; without that case it would
    # never be tested, and the culprit can perfectly well be the last one.
    lastline=$(grep -nE '^      in[[:space:]]*$' "$tmp/full.nix" | head -1 | cut -d: -f1)
    for i in "${!binds[@]}"; do
        start=${binds[$i]}
        if [ $((i + 1)) -lt "${#binds[@]}" ]; then
            end=$(( binds[$((i + 1))] - 1 ))
        elif [ -n "$lastline" ] && [ "$lastline" -gt "$start" ]; then
            end=$(( lastline - 1 ))
        else
            continue
        fi
        sed "${start},${end}d" "$tmp/full.nix" > "$tmp/cut.nix"
        if parses "$tmp/cut.nix"; then
            echo
            echo "CULPRIT: removing lines ${start}-${end} makes it parse:"
            sed -n "${start},${end}p" "$tmp/full.nix" | sed 's/^/    /'
            exit 0
        fi
    done
    # Removing one binding may not clear the failure if SEVERAL independently
    # break the parse. Invert the test: keep exactly ONE binding and drop the
    # rest, which names every culprit instead of only a sole one.
    echo
    echo "no single removal explains it — testing each binding in ISOLATION"
    inline2=$(grep -nE '^      in[[:space:]]*$' "$tmp/full.nix" | head -1 | cut -d: -f1)
    letline2=$(grep -nE '^      let[[:space:]]*$' "$tmp/full.nix" | head -1 | cut -d: -f1)
    culprits=0
    for i in "${!binds[@]}"; do
        start=${binds[$i]}
        if [ $((i + 1)) -lt "${#binds[@]}" ]; then
            end=$(( binds[$((i + 1))] - 1 ))
        else
            end=$(( inline2 - 1 ))
        fi
        # Delete the tail range first so the head range's numbers stay valid.
        { [ "$end" -lt $(( inline2 - 1 )) ] && printf '%s,%sd\n' "$((end + 1))" "$((inline2 - 1))"; \
          [ "$start" -gt $(( letline2 + 1 )) ] && printf '%s,%sd\n' "$((letline2 + 1))" "$((start - 1))"; } \
            > "$tmp/script.sed"
        sed -f "$tmp/script.sed" "$tmp/full.nix" > "$tmp/only.nix"
        if ! parses "$tmp/only.nix"; then
            culprits=$((culprits + 1))
            echo "  CULPRIT lines ${start}-${end}:"
            sed -n "${start},$(( start + 6 ))p" "$tmp/full.nix" | sed 's/^/      /'
            echo
        fi
    done
    if [ "$culprits" -gt 0 ]; then
        echo "  $culprits binding(s) independently break the parse (see above)"
        exit 0
    fi

    echo
    echo "no single top-level binding explains it — isolating by region"

    # Fall back to region isolation: blank out the per-system let BODY, then the
    # return attrset BODY, and see which one clears the failure. That separates
    # "some construct inside the let" from "some construct inside the returned
    # attrset" from "the head/arg structure itself".
    letline=$(grep -nE '^      let[[:space:]]*$' "$tmp/full.nix" | head -1 | cut -d: -f1)
    inline=$(grep -nE '^      in[[:space:]]*$' "$tmp/full.nix" | head -1 | cut -d: -f1)
    openline=$(( inline + 1 ))
    closeline=$(grep -nE '^      \}[[:space:]]*$' "$tmp/full.nix" | tail -1 | cut -d: -f1)
    echo "  let=$letline in=$inline return-open=$openline return-close=$closeline"

    if [ -n "$letline" ] && [ -n "$inline" ] && [ "$inline" -gt "$((letline + 1))" ]; then
        sed "$((letline + 1)),$((inline - 1))d" "$tmp/full.nix" > "$tmp/nolet.nix"
        if parses "$tmp/nolet.nix"; then
            echo "  => emptying the LET body fixes it: the culprit is inside the let block"
            exit 0
        fi
        echo "  => emptying the LET body does NOT fix it"
    fi

    if [ -n "$closeline" ] && [ "$closeline" -gt "$((openline + 1))" ]; then
        sed "$((openline + 1)),$((closeline - 1))d" "$tmp/full.nix" > "$tmp/noret.nix"
        if parses "$tmp/noret.nix"; then
            echo "  => emptying the RETURN attrset fixes it: the culprit is in the returned attrs"
            exit 0
        fi
        echo "  => emptying the RETURN attrset does NOT fix it"
    fi

    echo "  => neither region alone explains it; suspect the arg set / head structure" >&2
    exit 1

# --- verify ---

verify: verify-linter-fixtures verify-no-remarshal verify-flakeedit-parse verify-flakeclobber-parse

# Behavioral fixture tests for the nix/linters/ whole-tree checks: build the
# `linter-fixtures` aggregate, which runs each compiled linter against pass/fail
# fixture trees and asserts the exit code + output token (nix/linter-fixtures.nix,
# conformist#17). Builds only the aggregate — NOT a full `nix flake check`, which
# would also realize the ~130 registry smoke checks.
#
# run the whole-tree linter behavioral fixtures
verify-linter-fixtures:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.linter-fixtures" --no-link --print-build-logs

# Guard the conformist#60 fix: `pkgs.formats.toml` / `pkgs.formats.yaml` serialize
# via remarshal, whose closure drags matplotlib -> ffmpeg into EVERY generated
# config as a build-time dep. All TOML/YAML config generation must go through
# mkTomlFormat / mkYamlFormat (defined in nix/default.nix — the ONLY sanctioned
# reference, since they reuse `pkgs.formats.<fmt>.type` for validation). Fail if
# any other nix file reaches for the remarshal-backed generators directly, so the
# ffmpeg chain cannot creep back in. Source-level grep (the remarshal dep is a
# build-time, not runtime, closure member, so it can't be asserted against a
# realized closure).
#
# guard against remarshal-backed format generators outside nix/default.nix
verify-no-remarshal:
    #!/usr/bin/env bash
    set -euo pipefail
    # `|| true` on the whole pipeline: grep exits 1 on zero matches (the clean
    # state), which would otherwise abort under `set -o pipefail`.
    hits=$(grep -rnE 'pkgs\.formats\.(toml|yaml)' nix --include='*.nix' \
        | grep -vE '^nix/default\.nix:' || true)
    if [ -n "$hits" ]; then
        echo "verify-no-remarshal: remarshal-backed pkgs.formats.{toml,yaml} used outside nix/default.nix (conformist#60):" >&2
        echo "$hits" >&2
        echo "Use mkTomlFormat / mkYamlFormat (passed via defaultSpecialArgs) instead." >&2
        exit 1
    fi
    echo "verify-no-remarshal: no remarshal-backed format generators outside the sanctioned helper"

# Parse-safety smoke for the in-place flake editor (conformist#63): run `conform`
# over the recognized-shape fixtures in test/flakeedit/ in throwaway temp dirs and
# `nix-instantiate --parse` each rewritten flake, so a flakeedit splice regression
# that yields unparseable Nix fails the lane. Syntax-only (--parse, no eval), so
# it stays cheap. --force-formatter exercises the value-replacement splice too.
#
# check the flake editor's rewrites stay parseable Nix
verify-flakeedit-parse:
    #!/usr/bin/env bash
    set -euo pipefail
    bin="$(mktemp -d)/conformist"
    nix develop --command go build -o "$bin" .
    shopt -s nullglob
    fixtures=(test/flakeedit/*.nix)
    if [ "${#fixtures[@]}" -eq 0 ]; then
        echo "verify-flakeedit-parse: no fixtures in test/flakeedit/" >&2
        exit 2
    fi
    for f in "${fixtures[@]}"; do
        d="$(mktemp -d)"
        cp "$f" "$d/flake.nix"
        rc=0
        ( cd "$d" && "$bin" conform --force-formatter ) >/dev/null 2>&1 || rc=$?
        # conform exits 0 (no change) or 3 (edited); 2 is an operational error.
        if [ "$rc" = 2 ]; then
            echo "verify-flakeedit-parse: conform errored on $f" >&2
            exit 1
        fi
        if ! nix-instantiate --parse "$d/flake.nix" >/dev/null; then
            echo "verify-flakeedit-parse: edited $f is not parseable Nix" >&2
            exit 1
        fi
        echo "verify-flakeedit-parse: ok ($(basename "$f"))"
    done

# End-to-end gate for flakeclobber (conformist#99), the fleet-migration sweeper.
# Runs the REAL BINARY over test/flakeclobber/ fixtures in throwaway temp dirs and
# asserts, per the RFC 0004 §5 matrix, both the exit code AND what happened to the
# file on disk — then `nix-instantiate --parse`s every rewrite.
#
# Exercising main.go end-to-end is the whole point: every Go test calls Clobber()
# directly, so the shipped --apply path had NO coverage at all and a parse gate
# that could never pass went green through a full CI lane. A unit
# test cannot catch that class of defect; only running the binary can.
#
# check flakeclobber's rewrites apply and stay parseable Nix
verify-flakeclobber-parse:
    #!/usr/bin/env bash
    set -euo pipefail
    bin="$(mktemp -d)/flakeclobber"
    nix develop --command go build -o "$bin" ./cmd/flakeclobber

    fail=0
    # fixture:expected-exit:expected-disk-state
    checks=(
        "old-wiring.nix:0:changed"
        "already-migrated.nix:0:unchanged"
        "no-just.nix:0:unchanged"
        "no-devshell.nix:1:unchanged"
        "unrecognized.nix:1:unchanged"
    )
    for entry in "${checks[@]}"; do
        IFS=: read -r fixture want_rc want_state <<< "$entry"
        src="test/flakeclobber/$fixture"
        if [ ! -e "$src" ]; then
            echo "verify-flakeclobber-parse: missing fixture $src" >&2
            exit 2
        fi
        d="$(mktemp -d)"
        cp "$src" "$d/flake.nix"
        before="$(sha256sum < "$d/flake.nix")"

        rc=0
        out="$("$bin" --apply --old pkgs.just --new justPkg "$d/flake.nix" 2>&1)" || rc=$?
        after="$(sha256sum < "$d/flake.nix")"

        state=unchanged
        [ "$before" = "$after" ] || state=changed

        if [ "$rc" != "$want_rc" ]; then
            echo "verify-flakeclobber-parse: $fixture: exit $rc, want $want_rc" >&2
            echo "$out" | sed 's/^/    /' >&2
            fail=1
        fi
        if [ "$state" != "$want_state" ]; then
            echo "verify-flakeclobber-parse: $fixture: file $state, want $want_state" >&2
            echo "$out" | sed 's/^/    /' >&2
            fail=1
        fi
        # A rewrite that is not parseable Nix is the regression this gate exists
        # to catch; an unchanged file is re-parsed too, proving the fixture itself
        # is valid and the assertion above is meaningful.
        if ! nix-instantiate --parse "$d/flake.nix" >/dev/null; then
            echo "verify-flakeclobber-parse: $fixture: result is not parseable Nix" >&2
            fail=1
        fi
        # A non-silent report is part of the contract: a file that prints nothing
        # is indistinguishable from a successful migration in a 34-repo sweep log.
        if [ -z "$out" ]; then
            echo "verify-flakeclobber-parse: $fixture: produced NO output" >&2
            fail=1
        fi
        [ "$fail" = 0 ] && echo "verify-flakeclobber-parse: ok ($fixture: exit $rc, $state)"
    done

    # The conformist#100 hazard: replacing pkgs.just with an identifier nothing
    # binds must be REFUSED, not written. --parse cannot catch it (syntax-only),
    # so this asserts the static binding check does.
    d="$(mktemp -d)"
    cp test/flakeclobber/old-wiring.nix "$d/flake.nix"
    before="$(sha256sum < "$d/flake.nix")"
    rc=0
    out="$("$bin" --apply --old pkgs.just --new noSuchBinding "$d/flake.nix" 2>&1)" || rc=$?
    if [ "$rc" = 0 ] || [ "$before" != "$(sha256sum < "$d/flake.nix")" ]; then
        echo "verify-flakeclobber-parse: unbound replacement was NOT refused (exit $rc)" >&2
        echo "$out" | sed 's/^/    /' >&2
        fail=1
    else
        echo "verify-flakeclobber-parse: ok (unbound replacement refused, exit $rc)"
    fi

    # A missing --new must be an error, never an implicit deletion.
    rc=0
    out="$("$bin" --apply --old pkgs.just "$d/flake.nix" 2>&1)" || rc=$?
    if [ "$rc" != 2 ]; then
        echo "verify-flakeclobber-parse: bare --old should exit 2, got $rc" >&2
        echo "$out" | sed 's/^/    /' >&2
        fail=1
    else
        echo "verify-flakeclobber-parse: ok (unpaired --old refused, exit 2)"
    fi

    exit "$fail"

# OPT-IN: drift check for the committed godyn-graph.json — regenerate the graph
# into a scratch file and diff it against the committed copy, failing if they
# differ. godyn is opt-in now (bga is the default backend), so this no longer
# gates the merge; it's a manual check for the godyn path — hence the debug group,
# not the `verify` pipeline lane. MUST run on x86_64-linux — on another host
# godyn-gen emits a host-platform graph that always "differs" from the
# linux-locked committed one (a false positive; igloo#33). Keeps the working tree
# untouched (unlike debug-godyn-graph, which writes in place).
#
# check the committed godyn-graph.json for drift
[group("debug")]
debug-godyn-graph-drift:
    #!/usr/bin/env bash
    set -euo pipefail
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT
    nix develop --command env CGO_ENABLED=0 godyn-gen . "$tmp"
    if ! diff -u godyn-graph.json "$tmp"; then
        echo "debug-godyn-graph-drift: committed godyn-graph.json is stale — run 'just debug-godyn-graph' and commit the result." >&2
        exit 1
    fi

# Trace why a build pulls in a package — the diagnostic for the closure-bloat
# work (conformist#60). Builds TARGET (default the `nix fmt` wrapper, i.e. the
# cold-build a consumer pays for) and reports members matching NEEDLE (e.g.
# `ffmpeg`, `matplotlib`, `python3`) in BOTH the runtime closure (what
# `path-info -r` / `why-depends` see) AND the build-time `.drv` requisite closure
# (the deps a cold build realizes — e.g. a tool's checkPhase test deps — which
# slow the build but are NOT runtime-closure members, so a runtime-only trace
# misses them). For a devShell pass `.inputDerivation`: a bare `nix build` of an
# mkShell yields a near-empty $out whose runtime closure omits the dev tools.
# Mirrors papi's `debug-why-depends ffmpeg-headless`. Diagnostic only — not in any
# aggregate / the CI lane.
#
# trace why a build pulls NEEDLE into its runtime and build-time closures
[group("debug")]
debug-why-depends needle target="":
    #!/usr/bin/env bash
    set -euo pipefail
    tgt="{{ target }}"
    if [ -z "$tgt" ]; then
        sys=$(nix eval --raw --impure --expr 'builtins.currentSystem')
        tgt=".#formatter.$sys"
    fi

    out=$(nix build --no-link --print-out-paths "$tgt")
    rt=$(nix path-info -r "$out" | grep -- "{{ needle }}" || true)
    if [ -n "$rt" ]; then
        echo "RUNTIME closure members of $tgt matching '{{ needle }}':"
        echo "$rt"
        echo
        echo "--- why-depends (runtime) chain to the first match ---"
        nix why-depends "$out" "$(echo "$rt" | head -1)"
    else
        echo "debug-why-depends: no RUNTIME closure member of $tgt matches '{{ needle }}'"
    fi
    echo

    drv=$(nix path-info --derivation "$tgt")
    bt=$(nix-store --query --requisites "$drv" | grep -- "{{ needle }}" || true)
    if [ -n "$bt" ]; then
        echo "BUILD-TIME (.drv) requisites of $tgt matching '{{ needle }}':"
        echo "$bt"
        echo
        echo "--- why-depends --derivation chain to the first build-time match ---"
        nix why-depends --derivation "$drv" "$(echo "$bt" | head -1)" || true
    else
        echo "debug-why-depends: no BUILD-TIME requisite of $tgt matches '{{ needle }}'"
    fi

# Verify a remarshal-free TOML serializer (yj) round-trips conformist's generated
# config identically before swapping it in (conformist#60). Builds the real
# impure self-config (remarshal-generated, exercises arrays/bools/nested tables/
# repair-command), extracts its canonical data as JSON, re-serializes that JSON to
# TOML with BOTH remarshal (incumbent) and `yj -jt` (candidate), reparses each back
# to JSON with a neutral parser, and asserts semantic equality. Finally feeds the
# yj-generated TOML to conformist itself to confirm it parses (exit 0/1 = parsed;
# 2 = config/operational error). Diagnostic only — not in any aggregate / the CI lane.
#
# check yj round-trips the generated TOML config identically to remarshal
[group("debug")]
debug-toml-roundtrip:
    #!/usr/bin/env bash
    set -euo pipefail
    sys=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    ref=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
    conf=$(nix build --no-link --print-out-paths ".#packages.$sys.default")/bin/conformist
    # Put the converters on PATH via one `nix shell` and call them by name (a
    # multi-output package like jq breaks `$(print-out-paths)/bin/x`). The body is
    # one single-quoted `bash -c` arg — no nested heredoc (its terminator must be
    # unindented, which would end the just recipe) and no apostrophes inside.
    # remarshal is already realized (it generated the ref config); yj/jq are tiny.
    nix shell nixpkgs#yj nixpkgs#remarshal nixpkgs#jq --command bash -c '
        set -euo pipefail
        ref="$1"; conf="$2"
        tmp=$(mktemp -d); trap "rm -rf \"$tmp\"" EXIT

        # Canonical data the config encodes (parsed by the incumbent, remarshal).
        remarshal -if toml -of json < "$ref" | jq -S . > "$tmp/in.json"

        # Re-serialize that JSON to TOML two ways.
        remarshal -if json -of toml < "$tmp/in.json" > "$tmp/remarshal.toml"
        yj -jt                      < "$tmp/in.json" > "$tmp/yj.toml"

        # Reparse each TOML back to canonical JSON (neutral parser = remarshal) and
        # compare. Semantic equality is what matters; byte differences (key order,
        # quoting, array layout) are fine if conformist parses them the same.
        remarshal -if toml -of json < "$tmp/remarshal.toml" | jq -S . > "$tmp/remarshal.rt.json"
        remarshal -if toml -of json < "$tmp/yj.toml"        | jq -S . > "$tmp/yj.rt.json"

        fail=0
        if diff -u "$tmp/in.json" "$tmp/yj.rt.json"; then
            echo "OK: yj json-to-toml round-trips identically to the input data"
        else
            echo "MISMATCH: yj output reparses to different data than the input" >&2; fail=1
        fi
        if diff -u "$tmp/remarshal.rt.json" "$tmp/yj.rt.json"; then
            echo "OK: yj output is semantically identical to remarshal output"
        else
            echo "MISMATCH: yj vs remarshal semantic diff above" >&2; fail=1
        fi

        echo "--- informational: byte diff remarshal.toml vs yj.toml ---"
        diff -u "$tmp/remarshal.toml" "$tmp/yj.toml" || true

        echo "--- conformist parses the yj-generated TOML? ---"
        work=$(mktemp -d)
        set +e
        "$conf" check --config-file="$tmp/yj.toml" --tree-root="$work" "$work"
        rc=$?
        set -e
        rm -rf "$work"
        # 0 (clean) or 1 (findings) means parsed fine; 2 means config/operational error.
        if [ "$rc" -eq 2 ]; then
            echo "MISMATCH: conformist rejected the yj-generated TOML (exit 2)" >&2; fail=1
        else
            echo "OK: conformist accepted the yj-generated TOML (exit $rc)"
        fi
        exit "$fail"
    ' bash "$ref" "$conf"

# Verify `yj -jy` round-trips a YAML settings config identically before swapping
# it in for remarshal (conformist#60) — the YAML sibling of debug-toml-roundtrip.
# Uses a representative settings shape (nested maps, lists, bools, ints, strings),
# since no consumer enables a yaml-config tool to harvest a real one. Serializes
# to YAML with both remarshal (incumbent) and yj, reparses each back to JSON with
# a neutral parser, and asserts semantic equality. Diagnostic only — not in any
# aggregate / the CI lane.
#
# check yj round-trips a YAML settings config identically to remarshal
[group("debug")]
debug-yaml-roundtrip:
    #!/usr/bin/env bash
    set -euo pipefail
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    printf '%s\n' '{"extends":"default","rules":{"line-length":{"max":120},"trailing-spaces":{}},"ignore":["foo/","bar.yaml"],"strict":true,"depth":3}' > "$tmp/in.json"
    nix shell nixpkgs#yj nixpkgs#remarshal nixpkgs#jq --command bash -c '
        set -euo pipefail
        tmp="$1"
        jq -S . < "$tmp/in.json" > "$tmp/in.norm.json"
        remarshal -if json -of yaml < "$tmp/in.json" > "$tmp/remarshal.yaml"
        yj -jy                      < "$tmp/in.json" > "$tmp/yj.yaml"
        remarshal -if yaml -of json < "$tmp/remarshal.yaml" | jq -S . > "$tmp/remarshal.rt.json"
        remarshal -if yaml -of json < "$tmp/yj.yaml"        | jq -S . > "$tmp/yj.rt.json"
        fail=0
        if diff -u "$tmp/in.norm.json" "$tmp/yj.rt.json"; then
            echo "OK: yj json-to-yaml round-trips identically to the input data"
        else
            echo "MISMATCH: yj yaml reparses to different data than the input" >&2; fail=1
        fi
        if diff -u "$tmp/remarshal.rt.json" "$tmp/yj.rt.json"; then
            echo "OK: yj output is semantically identical to remarshal output"
        else
            echo "MISMATCH: yj vs remarshal semantic diff above" >&2; fail=1
        fi
        echo "--- informational: byte diff remarshal.yaml vs yj.yaml ---"
        diff -u "$tmp/remarshal.yaml" "$tmp/yj.yaml" || true
        exit "$fail"
    ' bash "$tmp"

# Reproducer for the flakeclobber --apply parse gate never passing,
# so no file is ever written. Isolates the four ways nix-instantiate can be handed
# a source (path / /dev/stdin from a redirect / /dev/stdin from a PIPE / `-` from a
# pipe) — Go gives a non-*os.File Stdin an os.Pipe, so nix resolves /dev/stdin to
# /proc/N/fd/pipe:[…], which is not a path — then runs the real binary end-to-end
# and reports whether the target file actually changed on disk. Diagnostic only —
# the standing gate is verify-flakeclobber-parse.
#
# reproduce the flakeclobber --apply parse-gate failure
[group("debug")]
debug-flakeclobber-stdin:
    #!/usr/bin/env bash
    set -uo pipefail
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    cp test/flakeclobber/old-wiring.nix "$tmp/probe.nix"

    echo "=== nix-instantiate --parse input modes (nix $(nix --version | awk '{print $3}')) ==="
    nix-instantiate --parse "$tmp/probe.nix" >/dev/null 2>&1
    echo "  --parse <file path>            exit=$?"
    nix-instantiate --parse /dev/stdin < "$tmp/probe.nix" >/dev/null 2>&1
    echo "  --parse /dev/stdin < file      exit=$?"
    cat "$tmp/probe.nix" | nix-instantiate --parse /dev/stdin >/dev/null 2>&1
    echo "  --parse /dev/stdin from PIPE   exit=$?"
    cat "$tmp/probe.nix" | nix-instantiate --parse - >/dev/null 2>&1
    echo "  --parse - from PIPE            exit=$?"

    echo
    echo "=== flakeclobber --apply end-to-end (does the file change on disk?) ==="
    bin="$tmp/flakeclobber"
    nix develop --command go build -o "$bin" ./cmd/flakeclobber
    cp test/flakeclobber/old-wiring.nix "$tmp/flake.nix"
    before=$(sha256sum < "$tmp/flake.nix")
    "$bin" --apply --old pkgs.just --new justPkg "$tmp/flake.nix"
    echo "  flakeclobber exit=$?"
    after=$(sha256sum < "$tmp/flake.nix")
    if [ "$before" = "$after" ]; then
        echo "  RESULT: file is BYTE-IDENTICAL — the write never happened"
    else
        echo "  RESULT: file was rewritten"
        grep -n 'justPkg\|pkgs.just' "$tmp/flake.nix"
    fi

# Measure flakeclobber's recognition coverage across the real fleet: DRY-RUN the
# binary over every repo's top-level flake.nix under ROOT and tally the outcomes
# (would-migrate / already-migrated / not-applicable / unrecognized / no-devshell).
# This is the number that says whether a parser change actually bought coverage —
# the corpus is the fleet, not the fixtures. Read-only: never passes --apply.
# Diagnostic only; the standing gate is verify-flakeclobber-parse.
#
# tally flakeclobber recognition across the fleet's flake.nix files
[group("debug")]
debug-flakeclobber-coverage root="/home/sasha/eng/repos":
    #!/usr/bin/env bash
    set -uo pipefail
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    bin="$tmp/flakeclobber"
    nix develop --command go build -o "$bin" ./cmd/flakeclobber || exit 2

    : > "$tmp/log"
    total=0
    for f in "{{ root }}"/*/flake.nix; do
        [ -e "$f" ] || continue
        total=$((total + 1))
        repo=$(basename "$(dirname "$f")")
        out=$("$bin" --old pkgs.just --new justPkg "$f" 2>&1)
        case "$out" in
            *"not the recognized eachDefaultSystem shape"*) verdict=unrecognized ;;
            *"no devShells.default packages list"*)         verdict=no-devshell ;;
            *"is neither a let binding nor an outputs argument"*) verdict=unbound-justPkg ;;
            *"[dry-run] replaced"*)                         verdict=would-migrate ;;
            *"already replaced"*)                           verdict=already-migrated ;;
            *"migration does not apply"*)                   verdict=not-applicable ;;
            "")                                             verdict=SILENT ;;
            *)                                              verdict=other ;;
        esac
        printf '%s\t%s\n' "$verdict" "$repo" >> "$tmp/log"
    done

    echo "=== flakeclobber coverage over {{ root }} ($total flake.nix files) ==="
    sort "$tmp/log" | awk -F'\t' '{c[$1]++} END{for (k in c) printf "  %-18s %d\n", k, c[k]}' | sort -k2 -rn
    echo
    echo "=== repos with pkgs.just on its own line (the migration population) ==="
    pop=0
    for f in "{{ root }}"/*/flake.nix; do
        [ -e "$f" ] || continue
        grep -qE '^[[:space:]]*pkgs\.just[[:space:]]*$' "$f" && pop=$((pop + 1))
    done
    echo "  $pop"
    echo
    echo "=== per-repo verdicts ==="
    sort "$tmp/log" | sed 's/^/  /'

    # Classify the refusals. A repo whose outputs are flake-parts, a raw
    # forAllSystems/genAttrs, or the `<attrs> // eachDefaultSystem (…)` hybrid is
    # LEGITIMATELY outside flakeedit's deliberately narrow roster (conformist#65)
    # and must be left alone. A repo that IS a plain eachDefaultSystem and still
    # fails to parse is a parser defect worth chasing — that distinction is the
    # only way to tell a coverage ceiling from a bug.
    echo
    echo "=== why each refusal refuses ==="
    while IFS=$'\t' read -r verdict repo; do
        [ "$verdict" = unrecognized ] || continue
        f="{{ root }}/$repo/flake.nix"
        # NOTE: the `//` hybrid (conformist#65) and the wrapping paren
        # (conformist#101) are now RECOGNIZED, so neither is a refusal reason
        # any more — do not re-add them here. Order matters: the specific
        # shapes are tested before the generic fallbacks, because a file can
        # match several greps at once (which is why these labels are a
        # heuristic, not proof — verify a surprising one by reading the file).
        if grep -qE '\}[[:space:]]*@[[:space:]]*[a-zA-Z_][a-zA-Z0-9_-]*[[:space:]]*:' "$f"; then
            why="@-pattern outputs arg (\`}@inputs:\`) — parser gap"
        elif grep -qE 'lib\.eachSystem|eachSystemMap' "$f"; then
            why="eachSystem variant, not eachDefaultSystem (out of roster)"
        elif grep -qE '^[[:space:]]*rec[[:space:]]*\{' "$f"; then
            why="rec { } per-system return — splicable but rec-scoped, refused"
        # A bare `in` line immediately followed by a bare `let` line. awk, not
        # grep: this is a two-LINE pattern, and `grep -E` does not interpret a
        # \n in the pattern (it warns "stray \ before n" and silently matches
        # nothing).
        elif awk '
                prev ~ /^[[:space:]]*in[[:space:]]*$/ && $0 ~ /^[[:space:]]*let[[:space:]]*$/ { found = 1 }
                { prev = $0 }
                END { exit !found }
            ' "$f"; then
            why="chained 'let … in let … in' per-system body — parser gap"
        # forAllSystems/genAttrs is checked BEFORE flake-parts: a repo often
        # merely DECLARES flake-parts as an input while its outputs are a raw
        # genAttrs flake (igloo), and testing for the string first mislabels it.
        # flake-parts proper is identified by its mkFlake call.
        elif grep -qE 'forAllSystems|genAttrs' "$f"; then
            why="raw forAllSystems/genAttrs (out of roster)"
        elif grep -q 'mkFlake' "$f"; then
            why="flake-parts (out of roster)"
        elif grep -q 'eachDefaultSystem' "$f"; then
            why="PLAIN eachDefaultSystem — IN roster, cause NOT identified"
        else
            why="no eachDefaultSystem (out of roster)"
        fi
        printf '  %-16s %s\n' "$repo" "$why"
    done < <(sort "$tmp/log")

    # The sweep is TWO passes and the order matters: `conform` adds the just-us
    # input and the justPkg let binding (additive), THEN flakeclobber swaps the
    # list element (destructive). Run destructive-first and the rewrite names an
    # identifier nothing binds — which --parse cannot catch. This second pass
    # measures the migration as it will actually be run, in throwaway temp dirs.
    conformbin="$tmp/conformist"
    nix develop --command go build -o "$conformbin" . || exit 2
    : > "$tmp/log2"; : > "$tmp/raw2"
    for f in "{{ root }}"/*/flake.nix; do
        [ -e "$f" ] || continue
        repo=$(basename "$(dirname "$f")")
        d="$tmp/sweep/$repo"; mkdir -p "$d"
        cp "$f" "$d/flake.nix"
        ( cd "$d" && "$conformbin" conform ) >/dev/null 2>&1 || true
        out=$("$bin" --old pkgs.just --new justPkg "$d/flake.nix" 2>&1)
        case "$out" in
            *"not the recognized eachDefaultSystem shape"*) verdict=unrecognized ;;
            *"no devShells.default packages list"*)         verdict=no-devshell ;;
            *"is neither a let binding nor an outputs argument"*) verdict=unbound-justPkg ;;
            *"[dry-run] replaced"*)                         verdict=would-migrate ;;
            *"already replaced"*)                           verdict=already-migrated ;;
            *"migration does not apply"*)                   verdict=not-applicable ;;
            "")                                             verdict=SILENT ;;
            *)                                              verdict=other ;;
        esac
        printf '%s\t%s\n' "$verdict" "$repo" >> "$tmp/log2"
        # Surface anything the classifier does not recognize verbatim, so an
        # unexpected outcome is never silently bucketed into "other".
        if [ "$verdict" = other ] || [ "$verdict" = SILENT ]; then
            printf '%s\t%s\n' "$repo" "$(echo "$out" | head -2 | tr '\n' ' ')" >> "$tmp/raw2"
        fi
    done
    echo
    if [ -s "$tmp/raw2" ]; then
        echo "=== raw messages for unclassified post-conform outcomes ==="
        sed 's/^/  /' "$tmp/raw2"
        echo
    fi
    echo "=== after conform (additive) THEN flakeclobber --new justPkg ==="
    sort "$tmp/log2" | awk -F'\t' '{c[$1]++} END{for (k in c) printf "  %-18s %d\n", k, c[k]}' | sort -k2 -rn
    echo
    sort "$tmp/log2" | sed 's/^/  /'

    # conform MERGES justPkg into the devShell packages list while pkgs.just is
    # still there, so a following `--new justPkg` sees both and refuses as
    # ambiguous. Post-conform the correct destructive op is therefore a DELETE
    # of the now-redundant pkgs.just, not a replace. This pass measures that —
    # it is the sweep path that actually completes.
    : > "$tmp/log3"
    for d in "$tmp/sweep"/*; do
        [ -d "$d" ] || continue
        repo=$(basename "$d")
        out=$("$bin" --old pkgs.just --new "" "$d/flake.nix" 2>&1)
        case "$out" in
            *"not the recognized eachDefaultSystem shape"*) verdict=unrecognized ;;
            *"no devShells.default packages list"*)         verdict=no-devshell ;;
            *"[dry-run] removed"*)                          verdict=would-migrate ;;
            *"already removed"*)                            verdict=already-migrated ;;
            *"migration does not apply"*)                   verdict=not-applicable ;;
            "")                                             verdict=SILENT ;;
            *)                                              verdict=other ;;
        esac
        printf '%s\t%s\n' "$verdict" "$repo" >> "$tmp/log3"
    done
    echo
    echo '=== after conform THEN flakeclobber --new "" (delete) — the completing path ==='
    sort "$tmp/log3" | awk -F'\t' '{c[$1]++} END{for (k in c) printf "  %-18s %d\n", k, c[k]}' | sort -k2 -rn

# --- test ---

test: test-go

# run the Go test suite (-tags test); fail if the working tree mutates mid-run (#15)
test-go:
    #!/usr/bin/env bash
    # Guard for conformist#15: the cmd integration tests run conformist against
    # $TMPDIR fixtures. The cmd TestMain sets GIT_CEILING_DIRECTORIES and
    # CONFORMIST_CEILING_DIRECTORIES so they can't escape into the worktree, but
    # fail loudly if the working tree is mutated during the run so a regression
    # can't hide in a commit. No `set -e`: capture the test result, always run
    # the tree check (even on test failure), then propagate the test status.
    # -tags test: dewey's test_ui package is behind a `test` build constraint.
    set -uo pipefail
    before=$(git status --porcelain)
    nix develop --command go test -tags test ./...
    rc=$?
    after=$(git status --porcelain)
    if [ "$before" != "$after" ]; then
        echo "test-go: working tree changed during tests — likely conformist#15 (tests escaped tree-root into the worktree). Recover with 'git checkout -- .'." >&2
        exit 1
    fi
    exit "$rc"

# --- format ---

codemod-fmt: codemod-fmt-conformist

# format conformist's own tree in place via `nix fmt` (repair mode)
codemod-fmt-conformist:
    nix fmt

# --- maintenance ---

# `go mod tidy`, then regenerate gomod2nix.toml (the && dependency)
update-go: && build-gomod2nix
    nix develop --command go mod tidy

# set CONFORMIST_VERSION in version.env (the single source of truth)
[group("maintenance")]
bump-version new_version:
    sed -E -i "s/^(export CONFORMIST_VERSION)=.*/\1={{ new_version }}/" version.env

# create, push, and verify a signed vX.Y.Z tag from version.env
[group("maintenance")]
tag message:
    #!/usr/bin/env bash
    set -euo pipefail
    . version.env
    tag="v${CONFORMIST_VERSION:?missing CONFORMIST_VERSION in version.env}"
    git tag -s -m "{{ message }}" "$tag"
    echo "Created tag: $tag"
    git push origin "$tag"
    echo "Pushed $tag"
    git tag -v "$tag"

# cut a release from master: changelog, bump-version commit, signed tag, fj release
[group("maintenance")]
release new_version:
    #!/usr/bin/env bash
    set -euo pipefail

    # Release only from the default branch.
    branch=$(git rev-parse --abbrev-ref HEAD)
    if [[ "$branch" != "master" ]]; then
        echo "release only allowed from master (on '$branch')" >&2
        exit 1
    fi

    # Generate the changelog BEFORE bump-version — the release-bump commit
    # MUST NOT appear in the changelog it announces.
    prev=$(git tag --sort=-v:refname -l "v*" | head -1)
    header="release v{{ new_version }}"
    if [[ -n "$prev" ]]; then
        summary=$(git log --format='- %s' "$prev"..HEAD)
        msg="$header"$'\n\n'"$summary"
    else
        msg="$header"
    fi

    just bump-version "{{ new_version }}"
    git add version.env
    git commit -m "$header"

    just tag "$msg"

    # fj release create is MUST; artifact upload is MAY.
    fj release create "$header" --tag "v{{ new_version }}" --body "$msg"

# --- clean ---

clean: clean-build

# remove the nix `result` symlink and the build/ output dir
clean-build:
    rm -rf result build/
