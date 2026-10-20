# This repo's conformist overlay, merged with conformist.lib.presets.eng and
# .eng-go in flake.nix. The presets enable the eng-convention linters and the
# canonical Go formatter chain (goimports, gofumpt); here: nixfmt for the
# flake itself, and the repo-specific tweaks.
{ ... }:
{
  programs.nixfmt.enable = true;

  # eng-versioning(7) derives the version key from go.mod / go.nix / Cargo.toml.
  # go.nix names no key (go.mod is rendered inside nix), so set it explicitly.
  # `conformist conform` fills in <YOUR_REPO>_VERSION (uppercase, `-`→`_`); it
  # must match version.env.
  linters.eng-versioning.key = "EXAMPLE_VERSION";

  # Prose and generated files are out of scope for code formatters.
  settings.excludes = [
    "*.md"
    "flake.lock"
    "LICENSE"
  ];
}
