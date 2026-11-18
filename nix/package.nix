{self, ...}: {
  perSystem = {
    inputs',
    lib,
    pkgs,
    ...
  }: {
    packages.default = (pkgs.buildGoModule.override {go = inputs'.devtools.packages.go;}) {
      pname = "hix";
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

      vendorHash = "sha256-3aKtF+kUeeFpbCaeK4fBXq2fH1mg2C3qsNzedS5ldJU=";

      env.CGO_ENABLED = 0;

      ldflags = [
        "-s"
        "-w"
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
        installShellCompletion --cmd hix \
          --zsh <($out/bin/hix completion zsh) \
          --bash <($out/bin/hix completion bash) \
          --fish <($out/bin/hix completion fish)
        wrapProgram $out/bin/hix --prefix PATH : ${lib.makeBinPath [
          pkgs.age
          pkgs.sops
          pkgs.ssh-to-age
        ]}
      '';

      meta = {
        description = "Build, activate and maintain a NixOS or nix-on-droid flake configuration";
        homepage = "https://github.com/stubbedev/hix";
        license = lib.licenses.mit;
        mainProgram = "hix";
        platforms = lib.platforms.linux;
      };
    };
  };
}
