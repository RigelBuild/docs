{ pkgs, config, ... }:
let
  flake = builtins.getFlake (toString ./.);
  packages = flake.packages.${pkgs.stdenv.hostPlatform.system};
in
{
  imports = [ flake.devenvModules.default ];
  packages = [ pkgs.bun packages.hk ];
  env.HK_STATE_DIR = "${config.devenv.state}/hk";
}
