# eng-versioning(7) conformance as a whole-tree conformist linter
# (passes-files=false): verifies version.env at the tree root declares
# `export <REPO>_VERSION=<semver>`. <REPO> is the explicit `key` option when set,
# else derived (uppercased, - -> _) from go.mod's module path, else from
# Cargo.toml's `[package].name` (conformist#29) — so Rust/Cargo repos can enable
# the language-agnostic check too. It reads only committed files (version.env,
# go.mod, Cargo.toml), so it runs in the sandboxed checks.formatting derivation
# as well as `nix fmt`. See amarbel-llc/conformist#14, #29 and eng-versioning(7).
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.eng-versioning;

  check = pkgs.writeShellApplication {
    name = "conformist-eng-versioning";
    runtimeInputs = with pkgs; [
      coreutils
      gawk
      gnugrep
    ];
    text = ''
      # cwd is the tree root (conformist runs whole-tree checks there); this
      # check takes no file arguments.
      [ -f version.env ] || {
        echo "eng-versioning(7): version.env missing at tree root" >&2
        exit 1
      }

      # Derive the canonical version key: explicit `key` option wins, else
      # go.mod's module path, else Cargo.toml's [package].name (conformist#29).
      # No directory/remote fallback: in the sandboxed checks.formatting lane the
      # cwd is a /nix/store/<hash>-source path and .git is absent, so a dirname
      # fallback would derive a wrong key silently — the `key` option is the
      # reliable escape hatch instead.
      override=${lib.escapeShellArg (if cfg.key == null then "" else cfg.key)}
      if [ -n "$override" ]; then
        expected=$override
      elif [ -f go.mod ]; then
        module=$(awk '/^module /{print $2; exit}' go.mod)
        repo=''${module##*/}
        expected=$(printf '%s' "$repo" | tr '[:lower:]-' '[:upper:]_')_VERSION
      elif [ -f Cargo.toml ]; then
        # First "name = \"...\"" inside the [package] table. Cargo emits basic
        # (double-quoted) strings, so match those.
        repo=$(awk '
          /^[[:space:]]*\[/ { in_pkg = ($0 ~ /^[[:space:]]*\[package\][[:space:]]*$/) }
          in_pkg && /^[[:space:]]*name[[:space:]]*=/ {
            if (match($0, /"[^"]*"/)) { print substr($0, RSTART + 1, RLENGTH - 2); exit }
          }
        ' Cargo.toml)
        [ -n "$repo" ] || {
          echo "eng-versioning(7): Cargo.toml present but [package].name not found; set linters.eng-versioning.key" >&2
          exit 1
        }
        expected=$(printf '%s' "$repo" | tr '[:lower:]-' '[:upper:]_')_VERSION
      else
        echo "eng-versioning(7): cannot derive version key (no go.mod or Cargo.toml at tree root); set linters.eng-versioning.key" >&2
        exit 1
      fi

      if ! grep -qE "^export ''${expected}=[0-9]+\.[0-9]+\.[0-9]+" version.env; then
        found=$(grep -oE '^export [A-Za-z0-9_]+_VERSION' version.env | head -1 || true)
        echo "eng-versioning(7): version.env must declare 'export ''${expected}=<semver>' (found: ''${found:-none})" >&2
        exit 1
      fi

      echo "eng-versioning(7): ''${expected} present and well-formed"
    '';
  };
in
{
  options.linters.eng-versioning = {
    enable = lib.mkEnableOption "the eng-versioning(7) whole-tree conformance check";

    key = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "JUST_US_VERSION";
      description = ''
        The canonical version variable name required in version.env (e.g.
        JUST_US_VERSION). When null, it is derived: go.mod module path ->
        Cargo.toml [package].name, uppercased with `-` -> `_` and suffixed
        `_VERSION`. Set this for repos where neither file is present, or to pin
        the key explicitly rather than rely on derivation.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.eng-versioning = {
      command = lib.getExe check;
      includes = [ "version.env" ];
      passes-files = false;
    };
  };
}
