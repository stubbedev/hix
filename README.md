# phix

Build, activate and maintain a NixOS or nix-on-droid flake configuration from
its checkout. Evaluation and builds run as you with `nix build` under a live
build tree; only activation runs as root.

```sh
phix switch        # build, show the closure diff, activate
phix switch --ask  # ... and confirm after the diff, before activating
phix upgrade       # update flake inputs, commit and push flake.lock, switch
phix diff [a] [b]  # package diff between generations (default: previous vs current)
phix generations   # running and boot generations, versions, closure sizes
phix secret        # edit, set or rotate a sops secret under secrets/
phix trust         # add an age, post-quantum age or ssh-ed25519 recipient and re-wrap secrets/*
phix mcp           # MCP server for nixpkgs, options, flakes, caches and docs
phix help          # every verb
```

Builds and substitutions are shown live, in the style of nix-output-monitor:
running builds as a dependency tree with their phase and elapsed time; each
path being substituted with a progress bar, bytes, speed and the substituter
it comes from (then its unpacking progress); an overall download bar with
throughput and an ETA; and a `Builds │ Downloads` table of running, done and
waiting counts. Until builds start, the status line says whether nix is
evaluating or querying caches. The run ends with `Finished at <time> after
<elapsed>`, what was built and fetched, and how much came from each
substituter. Build times are remembered in `~/.cache/phix/build-times.json`
and shown as estimates next to running builds.

`--diff never|always` skips the closure diff or makes a failing one fatal (by
default it only warns). Activation and hook output is condensed to warnings,
errors, unit changes and the bootloader status; the rest is printed only if
the step fails, or always with `-v`.

Commands that change the system, the flake or its secrets (every rebuild
verb, `rollback`, `gc`, `update`, `upgrade`, `trust`, `secret`) take a lock
in `~/.cache/phix/run.lock`; a second one fails at once, naming the pid that
holds it.

## MCP server

`phix mcp` serves a [Model Context Protocol](https://modelcontextprotocol.io)
server for the Nix ecosystem over stdio; it needs no phix config. It queries
live APIs (search.nixos.org, NixHub, FlakeHub, cache.nixos.org, the NixOS
wiki, nix.dev, Noogle, the nix-darwin option docs), so it is more current
than `nix search`.

```json
{ "mcpServers": { "nix": { "command": "phix", "args": ["mcp"] } } }
```

`--transport` picks how clients connect: `stdio` (the default), `http` (MCP
Streamable HTTP, on `127.0.0.1:8765/mcp`) or `sse` (the legacy HTTP+SSE
transport, on `127.0.0.1:8765/sse`), with `--addr` and `--path` to move them.
Over `http` and `sse` many clients share one server, a `/healthz` endpoint
answers, and a client pins the flake directory that `flake-inputs` works on
per request with an `X-Repo-Root` header; over stdio it is taken from the
client's MCP roots. With `PHIX_MCP_TOKEN` set, `http` and `sse` clients must
send it as `Authorization: Bearer <token>` or `X-Mcp-Token`.

Two tools are exposed:

- **`nix`** dispatches on `action`:
  - `search`: keyword search. `source` is `nixos` (default), `home-manager`,
    `darwin`, `flakes`, `flakehub`, `nixvim`, `wiki`, `nix-dev`, `noogle` or
    `nixhub`; for `nixos`, `type` is `packages`, `options`, `programs` or
    `flakes`.
  - `info`: details for an exact package, option, FlakeHub `org/project`,
    wiki or nix.dev page, Noogle function or NixHub package.
  - `stats`: counts per source.
  - `browse`: walk the nix-darwin or Noogle option tree by prefix.
  - `channels`: NixOS channels with their indexed and branch revisions.
  - `flake-inputs`: `type=list|ls|read` over a flake's inputs (runs
    `nix flake archive`).
  - `cache`: binary cache status of a package across systems.
  - `store`: `type=ls|read` of an absolute `/nix/store/` path.
- **`nix_versions`**: version history from NixHub: which nixpkgs commit
  shipped a version, its attribute path and platforms.

Nixvim's option docs moved to a WASM-decoded index, so `source=nixvim`
reports that it is unavailable rather than returning empty results.

## Runtime

phix builds for Linux and macOS (`x86_64-linux`, `aarch64-linux`,
`aarch64-darwin`), and CI builds and tests each one: `nix run
github:stubbedev/phix -- mcp` or `go install github.com/stubbedev/phix@latest`.
The rebuild verbs target NixOS and nix-on-droid; `phix mcp` needs no phix
config and runs on all of them.

phix drives `nix`, `git`, `sops`, `sudo` and `systemd-run`; the package puts
`sops` on its PATH. `trust` takes an age key (`age1…`), a post-quantum age key
(`age1pq1…`) or an `ssh-ed25519` public key, validates it in-process, and stores
it in canonical form. `whoami` derives this host's age recipient from its SSH
key in-process, so `age` and `ssh-to-age` are only needed by the test suite, which
checks the conversion against `ssh-to-age`.

## Home Manager

The flake exports `homeManagerModules.default`:

```nix
{
  imports = [ inputs.phix.homeManagerModules.default ];

  programs.phix = {
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

It renders `~/.config/phix/config.yaml`, which phix decodes strictly.
`commands` become `phix <name> [args]`, listed in help and completion.
`postSwitch` hooks run after a successful `switch` or `upgrade` with the
activated generation as their last argument and `PHIX_FLAKE` set;
`secretEnv` variables are decrypted from sops secrets under the flake's
`secrets/`, and a hook whose secret does not exist yet is skipped with a
warning.

`PHIX_CONFIG`, `PHIX_FLAKE_DIR` and `PHIX_NIXOS_CONFIG` override the config file,
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
