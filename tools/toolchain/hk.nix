{ pkgs }:
let
  version = "2.11.1";
  srcs = {
    "x86_64-linux" = {
      url = "https://github.com/jdx/hk/releases/download/v${version}/hk-x86_64-unknown-linux-musl.tar.gz";
      hash = "sha256-IGfMtO60yxdiOn+EDuJKZO3PokJDGwkDWKhU5hmz00U=";
    };
    "aarch64-linux" = {
      url = "https://github.com/jdx/hk/releases/download/v${version}/hk-aarch64-unknown-linux-musl.tar.gz";
      hash = "sha256-XiB0fhM1xZniwc9b/waoFTzUKnGa8AztIYhoeEh1pj8=";
    };
    "aarch64-darwin" = {
      url = "https://github.com/jdx/hk/releases/download/v${version}/hk-aarch64-apple-darwin.tar.gz";
      hash = "sha256-lfiebu9g4MaQsNQ5sw47hVj0cjWhpFy/97U1HSlOQyI=";
    };
  };
  src = srcs.${pkgs.stdenv.hostPlatform.system} or (throw "hk: unsupported platform ${pkgs.stdenv.hostPlatform.system}");
in
pkgs.stdenvNoCC.mkDerivation {
  pname = "hk";
  inherit version;
  src = pkgs.fetchurl src;
  dontUnpack = true;
  dontConfigure = true;
  dontBuild = true;
  installPhase = ''
    runHook preInstall
    install -d "$out/bin"
    tar -xzf "$src" -C "$out/bin" hk
    chmod 755 "$out/bin/hk"
    runHook postInstall
  '';
}
