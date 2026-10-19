# This repo's conformist overlay, merged with conformist.lib.presets.eng in
# flake.nix. The preset enables the eng-convention linters; here you choose the
# formatters and any repo-specific tweaks.
{ ... }:
{
  # Formatters this repo wants. nixfmt formats the flake itself, rustfmt the
  # crate's *.rs. See `man conformist.toml` and the conformist programs registry.
  programs.nixfmt.enable = true;
  programs.rustfmt.enable = true;

  # eng-versioning(7) derives the version key from Cargo.toml's [package] name,
  # which `conformist conform` filled in; version.env carries the matching key.

  # Prose and generated files are out of scope for code formatters.
  settings.excludes = [
    "*.md"
    "flake.lock"
    "LICENSE"
  ];
}
