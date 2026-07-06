# conformist's own config, consumed by self.lib.evalModule (see flake.nix).
# This replaces the former treefmt.nix: conformist now self-consumes its own Nix
# module instead of treefmt-nix (issue #4). Drives `nix fmt` (write mode via
# build.wrapper) and `nix build .#checks.<sys>.formatting` (read-only
# `conformist check` via build.check). `package` is injected by flake.nix.
{ ... }:
{
  projectRootFile = "flake.nix";

  # Formatters. Go is the canonical eng chain (goimports+gofumpt) via the eng-go
  # preset imported below — conformist dogfoods it instead of the plain gofmt it
  # used to be an outlier on (eng #18). nixfmt/taplo are conformist's own picks.
  programs.nixfmt.enable = true;
  programs.taplo.enable = true;

  # Linter (RFC 0001 §4): shellcheck inspects the shell in the justfile recipes
  # and any *.sh / *.bash / *.envrc in the tree, and dogfoods the [linter.*]
  # path end-to-end.
  linters.shellcheck.enable = true;

  # Whole-tree checks (passes-files=false): conformist self-enforces the eng-*
  # conventions via the pure preset it ships — the same roster a downstream repo
  # gets from `imports = [ conformist.lib.presets.eng ]`. These read only
  # committed files, so they run in the sandboxed checks.formatting gate (the
  # git-state checks live in nix/conformist-impure.nix, via the eng-impure preset).
  # eng-go adds the canonical Go formatter chain (goimports+gofumpt) — the same
  # chain a downstream Go repo gets from `imports = [ conformist.lib.presets.eng-go ]`.
  imports = [
    ./presets/eng.nix
    ./presets/eng-go.nix
  ];

  # Go-specific, so not in the language-agnostic preset (conformist#10: a
  # golangci-lint-gating repo must wire the dewey plugin via .custom-gcl.yml).
  linters.golangci-dewey.enable = true;

  # Prefer top-level `excludes` over the deprecated `global.excludes`. These
  # apply to formatters and linters alike, so the test/** fixtures (deliberately
  # mis-formatted) are not linted or format-checked.
  settings.excludes = [
    # Generated / locked — not hand-formatted. godyn-graph.json is emitted by
    # godyn-gen and its byte-exact form is asserted by debug-godyn-graph-drift, so a
    # formatter must never rewrite it.
    "gomod2nix.toml"
    "godyn-graph.json"
    "flake.lock"
    "go.sum"
    # conformist's test corpus contains files deliberately mis-formatted as
    # formatter-test fixtures; formatting them would corrupt the suite.
    "test/**"
    # templates/** is consumer-facing scaffold (the `nix flake init -t .#eng`
    # source), validated by instantiating it, not by conformist linting its own
    # copy — e.g. a direnv .envrc legitimately has no shebang (shellcheck SC2148).
    "templates/**"
    # Prose and design docs are out of scope for code formatters.
    "docs/**"
    "*.md"
    "LICENSE"
    "NOTICE"
  ];
}
