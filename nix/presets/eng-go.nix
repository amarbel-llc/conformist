# conformist eng preset (Go formatter chain): the single canonical Go formatter
# sequence the eng fleet has converged on — goimports (priority 1) then gofumpt
# (priority 2), so imports are grouped/added/pruned first and the result is
# re-canonicalized by gofumpt's stricter gofmt superset. A Go repo adopting
# conformist enables the whole chain with
#
#     imports = [
#       conformist.lib.presets.eng
#       conformist.lib.presets.eng-go
#     ];
#
# instead of hand-picking goimports / gofumpt / gofmt / golines in a per-repo
# order — the divergence this preset retires (eng #18: converge the Go formatter
# chains so every repo formats identically). Kept separate from the
# language-agnostic `eng` preset so a non-Go repo importing `eng` never pulls a
# Go formatter toolchain (gotools / gofumpt) it has no use for.
#
# Deliberately NOT enabled here:
#   - golines — line-wrapping is opt-in (no fleet repo runs it). A repo that
#     wants it sets `programs.golines.enable = true` with a priority after
#     gofumpt (e.g. 3), so golines wraps the already-canonicalized output;
#   - golangci-dewey — Go *linting* (the dewey plugin + .custom-gcl.yml wiring)
#     is a separate adoption decision from Go *formatting*; enable it explicitly
#     alongside this chain when the repo gates on golangci-lint (conformist#10).
#
# conformist self-consumes this preset (nix/conformist.nix), which is the only
# thing that forces it to evaluate in CI (checks.nix covers programs/linters, not
# presets) — so it can't drift from the chain conformist itself runs.
# See conformist-nix(7).
{ ... }:
{
  # goimports before gofumpt: goimports groups/adds/removes imports (its
  # formatting is gofmt-based), then gofumpt applies its stricter rules over the
  # import-grouped output. Formatters run in ascending priority order
  # (format/scheduler.go: formatterSortFunc), ties broken lexicographically — and
  # "gofumpt" sorts before "goimports" alphabetically, so these explicit
  # priorities are load-bearing, not decorative.
  programs.goimports.enable = true;
  programs.goimports.priority = 1;
  programs.gofumpt.enable = true;
  programs.gofumpt.priority = 2;
}
