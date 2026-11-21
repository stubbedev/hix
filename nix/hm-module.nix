{withSystem, ...}: {
  flake.homeManagerModules.default = {
    config,
    lib,
    pkgs,
    ...
  }: let
    cfg = config.programs.phix;

    choiceOptions = {
      name = lib.mkOption {
        type = lib.types.str;
        description = "The argument offered for completion.";
      };
      description = lib.mkOption {
        type = lib.types.str;
        description = "Its description in the completion menu.";
      };
    };

    commandType = lib.types.submodule {
      options =
        choiceOptions
        // {
          exec = lib.mkOption {
            type = lib.types.str;
            description = "Absolute path of the program `phix <name> [args]` runs with the arguments.";
          };
          complete = lib.mkOption {
            type = lib.types.listOf (lib.types.submodule {options = choiceOptions;});
            default = [];
            description = "Completions for the first argument.";
          };
        };
    };

    hookType = lib.types.submodule {
      options = {
        description = lib.mkOption {
          type = lib.types.str;
          description = "What the hook does, shown as its step in `phix switch`.";
        };
        exec = lib.mkOption {
          type = lib.types.str;
          description = "Absolute path of the program to run.";
        };
        args = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [];
          description = "Arguments before the activated generation, which phix always appends last.";
        };
        env = lib.mkOption {
          type = lib.types.attrsOf lib.types.str;
          default = {};
          description = "Environment variables set for the hook.";
        };
        secretEnv = lib.mkOption {
          type = lib.types.attrsOf lib.types.str;
          default = {};
          description = ''
            Environment variables decrypted from sops secrets under the
            flake's secrets/ directory, by secret name. phix skips the hook
            with a warning while one of them does not exist.
          '';
        };
      };
    };
  in {
    options.programs.phix = {
      enable = lib.mkEnableOption "phix, the flake configuration front-end";

      package = lib.mkOption {
        type = lib.types.package;
        default = withSystem pkgs.stdenv.hostPlatform.system ({config, ...}: config.packages.default);
        defaultText = lib.literalExpression "phix.packages.\${system}.default";
        description = "The phix package.";
      };

      flake = lib.mkOption {
        type = lib.types.str;
        description = "Directory of the flake checkout phix builds from.";
      };

      platform = lib.mkOption {
        type = lib.types.enum [
          "nixos"
          "android"
        ];
        description = ''
          What the configuration activates: a NixOS system or nix-on-droid.
          On "android", phix also runs the systemd user units home-manager
          generates: every login shell starts its supervisor (`phix service
          up`) and every activation applies the new units (`phix service
          sync`), the way systemd and sd-switch do on NixOS.
        '';
      };

      attr = lib.mkOption {
        type = lib.types.str;
        description = "Configuration attribute in the flake to build for this machine.";
      };

      sshKey = lib.mkOption {
        type = lib.types.str;
        default = "${config.home.homeDirectory}/.ssh/id_ed25519";
        defaultText = lib.literalExpression ''"''${config.home.homeDirectory}/.ssh/id_ed25519"'';
        description = "Private ed25519 key whose age identity decrypts the flake's sops secrets.";
      };

      commands = lib.mkOption {
        type = lib.types.listOf commandType;
        default = [];
        description = ''
          Extra subcommands: `phix <name> [args]` runs `exec` with the
          arguments, and `phix help` and the completion list them.
        '';
      };

      postSwitch = lib.mkOption {
        type = lib.types.listOf hookType;
        default = [];
        description = ''
          Hooks `phix switch` and `phix upgrade` run after a successful
          activation, with the activated generation as the last argument
          and PHIX_FLAKE set to the flake directory.
        '';
      };
    };

    config = lib.mkIf cfg.enable {
      home.packages = [cfg.package];

      home.sessionVariablesExtra = lib.mkIf (cfg.platform == "android") ''
        ${lib.getExe cfg.package} service up || printf '%s\n' "phix: user services are not running, see the error above" >&2
      '';

      home.activation.phixServices = lib.mkIf (cfg.platform == "android") (
        lib.hm.dag.entryAfter ["linkGeneration" "reloadSystemd"] ''
          run ${lib.getExe cfg.package} service sync
        ''
      );

      xdg.configFile."phix/config.yaml".source = (pkgs.formats.yaml {}).generate "phix-config.yaml" {
        inherit (cfg) flake platform attr commands;
        ssh_key = cfg.sshKey;
        hooks.post_switch =
          map (hook: {
            inherit (hook) description exec args env;
            secret_env = hook.secretEnv;
          })
          cfg.postSwitch;
      };
    };
  };
}
