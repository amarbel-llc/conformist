# A pure Nix library that handles the conformist configuration.
#
# Ported from treefmt-nix's default.nix (the formatter half is essentially
# verbatim) and extended with a parallel linter surface: `mkLinterModule` and
# the ./linters modules, which emit `[linter.<name>]` stanzas (RFC 0001 §4).
let
  # The base module configuration that generates and wraps the conformist config
  # with Nix.
  module-options = ./module-options.nix;

  # Program (formatter) to settings mapping.
  programs = import ./programs.nix;

  # Linter to settings mapping. Kept separate from programs so a formatter and
  # a linter MAY share a name (RFC 0001 §4) without the module system merging
  # their option declarations.
  linters = import ./linters.nix;

  # Sandbox-safe packaging for a script-based linter command (conformist#19).
  # `writeCheckScript pkgs { name; src; runtimeInputs ? [] }` installs the
  # script, patchShebangs it (so it execs in the pure-nix `conformist check`
  # sandbox), and optionally wraps a PATH. See ./write-check-script.nix.
  writeCheckScript = import ./write-check-script.nix;

  # A conformist wrapper carrying its formatter/linter toolchain on PATH
  # (conformist#51). `wrapWithToolchain pkgs { conformist; tools; name ?
  # "conformist"; configFile ? null }` builds a writeShellApplication that execs
  # conformist with `tools` on PATH, so a repo with a hand-written
  # conformist.toml gets a toolchain-hermetic `nix fmt` / `--staged` hook without
  # adopting the nix module. See ./wrap-with-toolchain.nix.
  wrapWithToolchain = import ./wrap-with-toolchain.nix;

  # The three named, toolchain-hermetic hook wrappers for a hand-written
  # conformist.toml — the TOML-consumer mirror of build.{wrapper,preCommit,
  # repair} (conformist#59). `mkToolchainHooks pkgs { conformist; tools;
  # configFile ? null; projectRootFile ? "flake.nix" }` returns
  # `{ formatter, preCommit, repair }` (named conformist / conformist-pre-commit
  # / conformist-repair), each execing conformist with `tools` on PATH. Use this
  # over wrapWithToolchain when you want the module-shaped named siblings (so the
  # sweatfile names `conformist-pre-commit` / `conformist-repair`) rather than a
  # single wrapper. See ./mk-toolchain-hooks.nix.
  mkToolchainHooks = import ./mk-toolchain-hooks.nix;

  # Remarshal-free config generators (conformist#60). nixpkgs'
  # `pkgs.formats.{toml,yaml}` serialize via `remarshal` (a Python tool) whose
  # build/test closure drags `matplotlib` -> `ffmpeg-headless` in as a BUILD-TIME
  # dependency of EVERY generated config — bloating every consumer's cold
  # `nix fmt` / `checks.formatting` build by hundreds of MB, even for a Nix+Go
  # repo (traced via `just debug-why-depends ffmpeg`). We keep
  # `pkgs.formats.<fmt>`'s `.type` (pure Nix — the value-validation schema, no
  # remarshal) and replace ONLY `.generate` with a `yj` json->fmt step (yj is a
  # tiny Go binary). Verified semantically identical to remarshal's output and
  # parseable by the consuming tool via `just debug-{toml,yaml}-roundtrip`.
  # mkTomlFormat / mkYamlFormat are passed to every module via defaultSpecialArgs
  # so the program/linter modules that emit their own TOML/YAML settings files
  # (statix, stylua, taplo, yamllint, …) drop remarshal too.
  mkYjFormat =
    { format, yjFlag }:
    pkgs:
    (pkgs.formats.${format} { })
    // {
      generate =
        name: value:
        pkgs.runCommandLocal name {
          nativeBuildInputs = [ pkgs.yj ];
          jsonText = builtins.toJSON value;
          passAsFile = [ "jsonText" ];
        } ''yj ${yjFlag} < "$jsonTextPath" > "$out"'';
    };
  mkTomlFormat = mkYjFormat {
    format = "toml";
    yjFlag = "-jt";
  };
  mkYamlFormat = mkYjFormat {
    format = "yaml";
    yjFlag = "-jy";
  };

  # mkFormatterModule builds a module that declares `programs.<name>.*` options
  # and, when enabled, emits a `[formatter.<name>]` stanza. Ported verbatim from
  # treefmt-nix so the ~155 programs/<name>.nix modules port unchanged.
  mkFormatterModule =
    {
      name,
      package ? name,
      mainProgram ? null,
      args ? [ ],
      includes ? [ ],
      excludes ? [ ],
      workingDir ? null,
    }:
    {
      pkgs,
      lib,
      config,
      options,
      ...
    }:
    let
      cfg = config.programs.${name};
      opt = options.programs.${name};
    in
    {
      options.programs.${name} = {
        enable = lib.mkEnableOption name;

        package = lib.mkPackageOption pkgs package { };

        includes = lib.mkOption {
          description = "Path / file patterns to include";
          type = lib.types.listOf lib.types.str;
          default = includes;
        };

        excludes = lib.mkOption {
          description = "Path / file patterns to exclude";
          type = lib.types.listOf lib.types.str;
          default = excludes;
        };

        priority = lib.mkOption {
          description = "Priority";
          type = lib.types.nullOr lib.types.int;
          default = null;
        };

        workingDir = lib.mkOption {
          description = "Subdirectory (relative to the tree root) to run the formatter in (conformist#38).";
          type = lib.types.nullOr lib.types.str;
          default = workingDir;
        };

        finalPackage = lib.mkOption {
          type = lib.types.package;
          readOnly = true;
          description = "Resulting `${name}` package bundled with plugins, if any.";
        };
      };

      config = lib.mkIf cfg.enable {
        settings.formatter.${name} = {
          command = lib.mkDefault (
            let
              pkg = if opt.finalPackage.isDefined then cfg.finalPackage else cfg.package;
            in
            if mainProgram == null then pkg else "${pkg}/bin/${mainProgram}"
          );
        }
        // (lib.optionalAttrs (args != [ ]) {
          options = if args._type or null == "order" then args else lib.mkBefore args;
        })
        // (lib.optionalAttrs (cfg.includes != [ ]) {
          inherit (cfg) includes;
        })
        // (lib.optionalAttrs (cfg.excludes != [ ]) {
          inherit (cfg) excludes;
        })
        // (lib.optionalAttrs (cfg.priority != null) {
          inherit (cfg) priority;
        })
        // (lib.optionalAttrs (cfg.workingDir != null) {
          "working-dir" = cfg.workingDir;
        });
      };
    };

  # mkLinterModule is the linter analog of mkFormatterModule. It declares
  # `linters.<name>.*` options and, when enabled, emits a `[linter.<name>]`
  # stanza (RFC 0001 §4). Differences from the formatter:
  #   - emits into settings.linter.<name>, not settings.formatter.<name>;
  #   - `command` is the read-only CHECK action;
  #   - adds optional repair-command / repair-options (the autofix action used
  #     in repair mode). The hyphenated TOML keys are quoted because that is the
  #     exact spelling conformist's config struct unmarshals
  #     (config/config.go: toml:"repair-command", toml:"repair-options").
  mkLinterModule =
    {
      name,
      package ? name,
      mainProgram ? null,
      args ? [ ],
      includes ? [ ],
      excludes ? [ ],
      workingDir ? null,
      # Repair (autofix) action, if the tool has one. The common case is the
      # SAME binary as the check `command` invoked with different args (e.g.
      # `statix check` vs `statix fix`, `ruff check` vs `ruff check --fix`): set
      # `repairArgs` and leave `repairMainProgram` null, and `repair-command`
      # defaults to the check binary. Set `repairMainProgram` only when the
      # autofix is a different executable. A linter with neither is a no-op in
      # repair mode (RFC 0001 §4).
      repairMainProgram ? null,
      repairArgs ? [ ],
    }:
    {
      pkgs,
      lib,
      config,
      options,
      ...
    }:
    let
      cfg = config.linters.${name};
      opt = options.linters.${name};
    in
    {
      options.linters.${name} = {
        enable = lib.mkEnableOption name;

        package = lib.mkPackageOption pkgs package { };

        includes = lib.mkOption {
          description = "Path / file patterns to include";
          type = lib.types.listOf lib.types.str;
          default = includes;
        };

        excludes = lib.mkOption {
          description = "Path / file patterns to exclude";
          type = lib.types.listOf lib.types.str;
          default = excludes;
        };

        priority = lib.mkOption {
          description = "Priority";
          type = lib.types.nullOr lib.types.int;
          default = null;
        };

        workingDir = lib.mkOption {
          description = "Subdirectory (relative to the tree root) to run the linter's check/repair in (conformist#38).";
          type = lib.types.nullOr lib.types.str;
          default = workingDir;
        };

        finalPackage = lib.mkOption {
          type = lib.types.package;
          readOnly = true;
          description = "Resulting `${name}` package bundled with plugins, if any.";
        };
      };

      config = lib.mkIf cfg.enable {
        settings.linter.${name} = {
          command = lib.mkDefault (
            let
              pkg = if opt.finalPackage.isDefined then cfg.finalPackage else cfg.package;
            in
            if mainProgram == null then pkg else "${pkg}/bin/${mainProgram}"
          );
        }
        // (lib.optionalAttrs (args != [ ]) {
          options = if args._type or null == "order" then args else lib.mkBefore args;
        })
        // (lib.optionalAttrs (repairMainProgram != null || repairArgs != [ ]) {
          # repair-command defaults to the check binary when repairMainProgram is
          # null (same-binary autofix, args differ via repairArgs); otherwise the
          # named repair executable. Unlike `command` (an exeType option that
          # getExe-coerces a package), this is a freeform string key, so resolve
          # the bare-package case with lib.getExe explicitly — otherwise it would
          # serialize the derivation's out path, not its binary.
          "repair-command" = lib.mkDefault (
            let
              pkg = if opt.finalPackage.isDefined then cfg.finalPackage else cfg.package;
              repairProg = if repairMainProgram == null then mainProgram else repairMainProgram;
            in
            if repairProg == null then lib.getExe pkg else "${pkg}/bin/${repairProg}"
          );
        })
        // (lib.optionalAttrs (repairArgs != [ ]) {
          "repair-options" =
            if repairArgs._type or null == "order" then repairArgs else lib.mkBefore repairArgs;
        })
        // (lib.optionalAttrs (cfg.includes != [ ]) {
          inherit (cfg) includes;
        })
        // (lib.optionalAttrs (cfg.excludes != [ ]) {
          inherit (cfg) excludes;
        })
        // (lib.optionalAttrs (cfg.priority != null) {
          inherit (cfg) priority;
        })
        // (lib.optionalAttrs (cfg.workingDir != null) {
          "working-dir" = cfg.workingDir;
        });
      };
    };

  all-modules =
    nixpkgs:
    [
      {
        _module.args = {
          pkgs = nixpkgs;
          lib = nixpkgs.lib;
        };
      }
      module-options
    ]
    ++ programs.modules
    ++ linters.modules;

  # conformist can be loaded into a submodule. In this case we get our `pkgs` from
  # our own standard option `pkgs`; not externally.
  submodule-modules = [
    (
      { config, lib, ... }:
      let
        inherit (lib)
          mkOption
          types
          ;
      in
      {
        options.pkgs = mkOption {
          type = types.uniq (types.lazyAttrsOf (types.raw or types.unspecified));
          description = ''
            Nixpkgs to use in `conformist`.
          '';
        };
        config._module.args = {
          pkgs = config.pkgs;
        };
      }
    )
    module-options
  ]
  ++ programs.modules
  ++ linters.modules;

  # Use the Nix module system to validate the conformist config file format.
  #
  # nixpkgs is an instance of <nixpkgs>.
  # configuration is an attrset used to configure the nix module.
  evalModule =
    nixpkgs: configuration:
    # NOTE: keep in sync with submoduleWith
    nixpkgs.lib.evalModules {
      modules = all-modules nixpkgs ++ [ configuration ];
      specialArgs = defaultSpecialArgs;
    };

  /**
    The built-in specialArgs for conformist-nix.
    These are module arguments passed to all conformist-nix modules.
  */
  defaultSpecialArgs = {
    inherit
      mkFormatterModule
      mkLinterModule
      mkTomlFormat
      mkYamlFormat
      ;
  };

  /**
    Invoke conformist-nix as a submodule, integrating this into a larger
    configuration management system.

    Unlike in `evalModule`, the caller is responsible for setting
    `_module.args.pkgs` inside the submodule.
  */
  submoduleWith =
    lib:
    {
      modules ? [ ],
      specialArgs ? { },
    }:
    # NOTE: keep in sync with evalModule
    lib.types.submoduleWith {
      modules = submodule-modules ++ modules;
      specialArgs = defaultSpecialArgs // specialArgs;
    };

  # Returns a conformist config file (TOML) generated from the passed
  # configuration.
  mkConfigFile =
    nixpkgs: configuration:
    let
      mod = evalModule nixpkgs configuration;
    in
    mod.config.build.configFile;

  # Returns an instance of conformist, wrapped with some configuration.
  mkWrapper =
    nixpkgs: configuration:
    let
      mod = evalModule nixpkgs configuration;
    in
    mod.config.build.wrapper;
in
{
  inherit
    module-options
    programs
    linters
    writeCheckScript
    wrapWithToolchain
    mkToolchainHooks
    mkTomlFormat
    mkYamlFormat
    all-modules
    submodule-modules
    evalModule
    submoduleWith
    mkConfigFile
    mkWrapper
    ;

  # Reusable config presets a consumer imports to enable a whole roster at once,
  # e.g. `imports = [ conformist.lib.presets.eng ];`. See nix/presets/.
  presets = {
    eng = ./presets/eng.nix;
    eng-go = ./presets/eng-go.nix;
    eng-impure = ./presets/eng-impure.nix;
  };
}
