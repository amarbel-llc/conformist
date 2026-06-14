# This repo's conformist overlay, merged with conformist.lib.presets.eng in
# flake.nix. The preset enables the eng-convention linters; here you choose the
# formatters and any repo-specific tweaks.
{ ... }:
{
  # Formatters this repo wants. nixfmt formats the flake itself; add your
  # language's formatter (gofmt, rustfmt, prettier, …) — see `man conformist.toml`
  # and the conformist programs registry.
  programs.nixfmt.enable = true;

  # eng-versioning(7) derives the version key from go.mod / Cargo.toml. This
  # language-agnostic template has neither, so set the key explicitly. Rename it
  # to <YOUR_REPO>_VERSION (uppercase, `-`→`_`) and match version.env.
  linters.eng-versioning.key = "EXAMPLE_VERSION";

  # Prose and generated files are out of scope for code formatters.
  settings.excludes = [
    "*.md"
    "flake.lock"
    "LICENSE"
  ];
}
