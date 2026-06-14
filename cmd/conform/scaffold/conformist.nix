# This repo's conformist overlay, merged with conformist.lib.presets.eng in
# flake.nix (see the snippet `conformist conform` printed). The preset enables the
# eng-convention linters; here you choose formatters and any repo-specific tweaks.
{ ... }:
{
  # Formatters this repo wants — add your language's (gofmt, rustfmt, prettier, …).
  # See `man conformist.toml` and the conformist programs registry.
  programs.nixfmt.enable = true;

  # eng-versioning(7) derives the version key from go.mod / Cargo.toml. If your
  # repo has neither, set it explicitly to match version.env:
  #   linters.eng-versioning.key = "<YOUR_REPO>_VERSION";

  # Prose and generated files are out of scope for code formatters.
  settings.excludes = [
    "*.md"
    "flake.lock"
    "LICENSE"
  ];
}
