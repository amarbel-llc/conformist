# deadnix as a conformist LINTER (RFC 0001 §4). The check action is
# `deadnix --fail` (reports dead Nix code, exits non-zero on findings — without
# `--fail` deadnix always exits 0, which would make the check a no-op gate);
# the repair action is `deadnix --edit` (removes it in repair mode). treefmt-nix
# shipped this as a "formatter" that always ran `--edit`; conformist splits the
# two (conformist#6).
#
# REPAIR HAZARD (conformist#88): deadnix analyzes one file at a time and cannot
# see a function's call sites. A lambda attrset pattern name that is unused in
# the function's own body (`{ goMod, src }: src`) is reported as dead, and
# `--edit` removes it — but when the pattern has no `...` and a caller still
# passes the attribute, the pruned function now fails with "called with
# unexpected argument". That is not provably-local dead code, so
# no-lambda-pattern-names DEFAULTS TO TRUE below: pattern names are excluded
# from both the check and the repair. Opting back in (false) re-enables the
# findings AND the unsafe repair edits — only do so on a tree where every
# pattern-name removal is hand-reviewed.
#
# The no-lambda-arg / no-lambda-pattern-names / no-underscore flags scope what
# deadnix considers dead, so they apply to BOTH the check and the repair
# invocation (appended to options and repair-options alike) — check and repair
# must agree on the finding set or a repair run leaves the check failing.
{
  lib,
  config,
  mkLinterModule,
  ...
}:
let
  cfg = config.linters.deadnix;
  scopeFlags =
    (lib.optional cfg.no-lambda-arg "--no-lambda-arg")
    ++ (lib.optional cfg.no-lambda-pattern-names "--no-lambda-pattern-names")
    ++ (lib.optional cfg.no-underscore "--no-underscore");
in
{
  meta.maintainers = [ ];

  imports = [
    (mkLinterModule {
      name = "deadnix";
      args = [ "--fail" ];
      repairArgs = [ "--edit" ];
      includes = [ "*.nix" ];
    })
  ];

  options.linters.deadnix = {
    no-lambda-arg = lib.mkEnableOption "Don't check lambda parameter arguments";
    no-lambda-pattern-names = lib.mkOption {
      description = ''
        Don't check lambda attrset pattern names (`{ arg, ... }:`).

        Defaults to true: deadnix cannot see call sites, so removing an
        in-body-unused name from a strict (no-`...`) pattern breaks any caller
        that still passes the attribute — `--edit` applied exactly that unsafe
        rewrite (conformist#88). Setting this to false re-enables pattern-name
        findings for BOTH check and repair; the repair is then unsafe on
        strict patterns with callers.
      '';
      type = lib.types.bool;
      default = true;
    };
    no-underscore = lib.mkEnableOption "Don't check any bindings that start with a _";
  };

  config = lib.mkIf (cfg.enable && scopeFlags != [ ]) {
    settings.linter.deadnix = {
      options = lib.mkAfter scopeFlags;
      "repair-options" = lib.mkAfter scopeFlags;
    };
  };
}
