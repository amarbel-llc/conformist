# conformist's own config, consumed by self.lib.evalModule (see flake.nix).
# This replaces the former treefmt.nix: conformist now self-consumes its own Nix
# module instead of treefmt-nix (issue #4). Drives `nix fmt` (write mode via
# build.wrapper) and `nix build .#checks.<sys>.formatting` (read-only
# `conformist check` via build.check). `package` is injected by flake.nix.
{ ... }:
{
  projectRootFile = "flake.nix";

  # Formatters.
  programs.gofmt.enable = true;
  programs.nixfmt.enable = true;
  programs.taplo.enable = true;

  # Linter (RFC 0001 §4): shellcheck inspects the shell in the justfile recipes
  # and any *.sh / *.bash / *.envrc in the tree, and dogfoods the [linter.*]
  # path end-to-end.
  linters.shellcheck.enable = true;

  # Whole-tree checks (passes-files=false): conformist self-enforces eng-*
  # conventions. These read only committed files, so they run in the sandboxed
  # checks.formatting gate (the git-state checks live in nix/conformist-impure.nix).
  linters.eng-versioning.enable = true; # eng-versioning(7): version.env key
  linters.eng-versioning-deprecated-file.enable = true; # ...(7): no version.txt / flake.nix named var (#14)
  linters.golangci-dewey.enable = true; # conformist#10: .custom-gcl.yml wires the dewey plugin
  linters.flake-outputs.enable = true; # conformist-nix(7) FLAKE OUTPUTS (#9): outputs formal accepts all inputs
  linters.flake-lock.enable = true; # conformist-nix(7) FLAKE HYGIENE (#11): flake.lock is committed
  linters.justfile-default.enable = true; # eng-design_patterns-justfile(7): default first
  linters.justfile-recipe-names.enable = true; # ...(7): verb-noun recipe naming

  # Prefer top-level `excludes` over the deprecated `global.excludes`. These
  # apply to formatters and linters alike, so the test/** fixtures (deliberately
  # mis-formatted) are not linted or format-checked.
  settings.excludes = [
    # Generated / locked — not hand-formatted. godyn-graph.json is emitted by
    # godyn-gen and its byte-exact form is asserted by verify-godyn-graph, so a
    # formatter must never rewrite it.
    "gomod2nix.toml"
    "godyn-graph.json"
    "flake.lock"
    "go.sum"
    # conformist's test corpus contains files deliberately mis-formatted as
    # formatter-test fixtures; formatting them would corrupt the suite.
    "test/**"
    # Prose and design docs are out of scope for code formatters.
    "docs/**"
    "*.md"
    "LICENSE"
    "NOTICE"
  ];
}
