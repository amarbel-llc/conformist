{
  lib,
  config,
  pkgs,
  mkFormatterModule,
  ...
}:
let
  cfg = config.programs.gofumpt;
  goNixField = import ../go-nix-field.nix { inherit pkgs; };

  # gofumpt defaults -lang and -modpath from the nearest go.mod. A go.nix module
  # (igloo FDR 0008) has no go.mod, so files whose nearest module root holds only
  # a go.nix get both from it; roots with a go.mod keep gofumpt's own discovery.
  # Explicit -lang/-modpath options are passed after, so they still win.
  wrapper = pkgs.writeShellApplication {
    name = "conformist-gofumpt";
    runtimeInputs = [
      pkgs.coreutils
      goNixField
    ];
    text = ''
      flags=()
      files=()
      while [ $# -gt 0 ]; do
        case $1 in
          -lang | -modpath)
            flags+=("$1" "$2")
            shift 2
            ;;
          -*)
            flags+=("$1")
            shift
            ;;
          *)
            files+=("$1")
            shift
            ;;
        esac
      done

      if [ ''${#files[@]} -eq 0 ]; then
        exec ${lib.getExe cfg.package} "''${flags[@]}"
      fi

      module_root() {
        local dir
        dir=$(dirname -- "$(realpath -s -- "$1")")
        while :; do
          if [ -f "$dir/go.mod" ] || [ -f "$dir/go.nix" ]; then
            printf '%s' "$dir"
            return 0
          fi
          if [ "$dir" = / ]; then
            return 0
          fi
          dir=$(dirname -- "$dir")
        done
      }

      declare -A seen=()
      roots=()
      file_roots=()
      for f in "''${files[@]}"; do
        root=$(module_root "$f")
        key=''${root:--}
        file_roots+=("$key")
        if [ -z "''${seen[$key]+x}" ]; then
          seen[$key]=1
          roots+=("$key")
        fi
      done

      rc=0
      for key in "''${roots[@]}"; do
        batch=()
        for i in "''${!files[@]}"; do
          if [ "''${file_roots[$i]}" = "$key" ]; then
            batch+=("''${files[$i]}")
          fi
        done

        manifest=()
        if [ "$key" != - ] && [ ! -f "$key/go.mod" ]; then
          if lang=$(conformist-go-nix-field go "$key/go.nix"); then
            manifest+=(-lang "go''${lang#go}")
          fi
          if modpath=$(conformist-go-nix-field module "$key/go.nix"); then
            manifest+=(-modpath "$modpath")
          fi
        fi

        ${lib.getExe cfg.package} "''${manifest[@]}" "''${flags[@]}" "''${batch[@]}" || rc=$?
      done
      exit "$rc"
    '';
  };
in
{
  meta.maintainers = [ "zimbatm" ];

  imports = [
    (mkFormatterModule {
      name = "gofumpt";
      args = [ "-w" ];
      includes = [ "*.go" ];
      excludes = [ "vendor/*" ];
    })
  ];

  options.programs.gofumpt = {
    extra = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Whether to enable extra rules.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.formatter.gofumpt = {
      command = lib.getExe wrapper;
      options = lib.optional cfg.extra "-extra";
      # The module manifests -lang/-modpath come from, shipped into check mode's
      # sandbox so check and repair agree (conformist#28).
      config-files = [
        "go.mod"
        "go.nix"
      ];
    };
  };
}
