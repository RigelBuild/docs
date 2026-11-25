{ pkgs, config, ... }:
let
  flake = builtins.getFlake (toString ./.);
  packages = flake.packages.${pkgs.stdenv.hostPlatform.system};
in
{
  imports = [ flake.devenvModules.default ];
  packages = [ pkgs.bun packages.hk packages.tools packages.go-dev ];
  env.HK_STATE_DIR = "${config.devenv.state}/hk";
  env.REF_GATE_BIN = "${packages.tools}/bin/ref-gate";
}
