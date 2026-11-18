# hix

Build, activate and maintain a NixOS or nix-on-droid flake configuration from
its checkout. Evaluation and builds run as you with `nix build` under a live
build tree; only activation runs as root.

```sh
hix switch        # build, show the closure diff, activate
hix switch --ask  # ... and confirm after the diff, before activating
hix upgrade       # update flake inputs, commit and push flake.lock, switch
hix diff [a] [b]  # package diff between generations (default: previous vs current)
hix generations   # running and boot generations, versions, closure sizes
hix secret        # edit, set or rotate a sops secret under secrets/
hix trust         # add an age recipient to .sops.yaml and re-wrap secrets/*
hix help          # every verb
```

Build times are remembered in `~/.cache/hix/build-times.json` and shown as
estimates next to running builds.

## Home Manager

The flake exports `homeManagerModules.default`:

```nix
{
  imports = [ inputs.hix.homeManagerModules.default ];

  programs.hix = {
    enable = true;
    flake = "/etc/nixos/dotfiles";
    platform = "nixos"; # or "android" for nix-on-droid
    attr = "desktop";
    commands = [
      {
        name = "clean";
        description = "Reclaim disk space";
        exec = lib.getExe myCleanScript;
      }
    ];
    postSwitch = [
      {
        description = "Push the system closure to the binary cache";
        exec = lib.getExe pushScript;
        secretEnv.PUSH_TOKEN = "cache-token";
      }
    ];
  };
}
```

It renders `~/.config/hix/config.yaml`, which hix decodes strictly.
`commands` become `hix <name> [args]`, listed in help and completion.
`postSwitch` hooks run after a successful `switch` or `upgrade` with the
activated generation as their last argument and `HIX_FLAKE` set;
`secretEnv` variables are decrypted from sops secrets under the flake's
`secrets/`, and a hook whose secret does not exist yet is skipped with a
warning.

`HIX_CONFIG`, `HIX_FLAKE_DIR` and `HIX_NIXOS_CONFIG` override the config file,
the flake directory and the attribute; `--attr` overrides both.

## Development

| Recipe | What it does |
| --- | --- |
| `just check` | Every release gate in order: vet, lint, test, build |
| `just vet` | Static analysis with `go vet` |
| `just lint` | `golangci-lint run` against `.golangci.yml` |
| `just test` | `go test ./...` |
| `just build` | Compile-check every package |
| `just fmt` | Format every Go source in place |

The Go toolchain comes from [stubbedev/flakes.nix](https://github.com/stubbedev/flakes.nix):
`direnv allow`, or `nix develop`. `nix build` builds the package
(`nix/package.nix`) and runs the tests; the dev shell is `nix/shell.nix`, the
Home Manager module `nix/hm-module.nix`.
