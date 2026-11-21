{self, ...}: {
  perSystem = {
    inputs',
    lib,
    pkgs,
    ...
  }: {
    packages.default = (pkgs.buildGoModule.override {go = inputs'.devtools.packages.go;}) (finalAttrs: {
      pname = "phix";
      version = "0-unstable-${self.lastModifiedDate or "0"}";

      src = lib.fileset.toSource {
        root = ../.;
        fileset = lib.fileset.difference (lib.fileset.gitTracked ../.) (
          lib.fileset.unions [
            ../flake.nix
            ../flake.lock
            ../nix
          ]
        );
      };

      vendorHash = "sha256-cTau23vwY0FffGBG+tGs6hnI3r8qN7tPiC1uOQ3A8Io=";

      env.CGO_ENABLED = 0;

      ldflags = [
        "-s"
        "-w"
        "-X github.com/stubbedev/phix/internal/version.Version=${finalAttrs.version}"
      ];

      nativeBuildInputs = [
        pkgs.installShellFiles
        pkgs.makeWrapper
      ];

      nativeCheckInputs = [
        pkgs.age
        pkgs.git
        pkgs.openssh
        pkgs.sops
        pkgs.ssh-to-age
      ];

      doCheck = true;

      preCheck = ''
        unformatted=$(find . -path ./vendor -prune -o -name '*.go' -print0 | xargs -0 gofmt -l)
        if [ -n "$unformatted" ]; then
          echo "gofmt: unformatted files: $unformatted" >&2
          exit 1
        fi
        go vet ./...
      '';

      postInstall = ''
        installShellCompletion --cmd phix \
          --zsh <($out/bin/phix completion zsh) \
          --bash <($out/bin/phix completion bash) \
          --fish <($out/bin/phix completion fish)
        wrapProgram $out/bin/phix --prefix PATH : ${lib.makeBinPath [pkgs.sops]}
      '';

      meta = {
        description = "Build, activate and maintain a NixOS or nix-on-droid flake configuration";
        homepage = "https://github.com/stubbedev/phix";
        license = lib.licenses.mit;
        mainProgram = "phix";
        platforms = lib.platforms.unix;
      };
    });
  };
}
