{
  description = "Public design documentation toolchain";

  inputs = {
    nixpkgs.url = "github:cachix/devenv-nixpkgs/rolling";
    # Not following nixpkgs: consumers share meissa's exact linter derivations.
    meissa.url = "github:RigelBuild/meissa/67893289152ae8b2e49f4860e457c14f017391ee";
  };

  outputs = { nixpkgs, meissa, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          meissaPackages = meissa.packages.${system};
        in {
          inherit (meissaPackages) rumdl biome tools go-dev;
          "rumdl-base-config" = meissaPackages."rumdl-base-config";
          hk = import ./tools/toolchain/hk.nix { inherit pkgs; };
          bun = pkgs.bun;
        });
      devenvModules.default = meissa.devenvModules.default;
    };
}
