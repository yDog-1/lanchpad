{
  description = "Lanchpad Discord bot and Go development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = self.packages.${system}.lanchpad;
          lanchpad = pkgs.buildGoModule {
            pname = "lanchpad";
            version = "0.0.0";
            src = pkgs.lib.fileset.toSource {
              root = ./.;
              fileset = pkgs.lib.fileset.unions [
                ./go.mod
                ./go.sum
                ./LICENSE
                (pkgs.lib.fileset.fileFilter (file: file.hasExt "go") ./.)
              ];
            };
            vendorHash = "sha256-NrkJF6hpFTflnowOUR7SR5kRF4OfbwKsxAsOtLqwRsg=";
            subPackages = [ "." ];
            doCheck = true;
            env.CGO_ENABLED = "0";
            ldflags = [
              "-s"
              "-w"
            ];
            meta = {
              description = "Discord bot for controlling PCs on a LAN";
              homepage = "https://github.com/yDog-1/lanchpad";
              license = pkgs.lib.licenses.mit;
              mainProgram = "lanchpad";
              platforms = systems;
            };
          };
        }
      );

      checks = forAllSystems (system: {
        inherit (self.packages.${system}) lanchpad;
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.gotools
              pkgs.nixfmt
              pkgs.nix-update
            ];
          };
        }
      );

      formatter = forAllSystems (system: nixpkgs.legacyPackages.${system}.nixfmt);
    };
}
