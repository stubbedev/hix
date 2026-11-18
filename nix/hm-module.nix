{withSystem, ...}: {
  flake.homeManagerModules.default = {
    config,
    lib,
    pkgs,
    ...
  }: let
    cfg = config.programs.hix;

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
            description = "Absolute path of the program `hix <name> [args]` runs with the arguments.";
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
          description = "What the hook does, shown as its step in `hix switch`.";
        };
        exec = lib.mkOption {
          type = lib.types.str;
          description = "Absolute path of the program to run.";
        };
        args = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [];
          description = "Arguments before the activated generation, which hix always appends last.";
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
            flake's secrets/ directory, by secret name. hix skips the hook
            with a warning while one of them does not exist.
          '';
        };
      };
    };
  in {
    options.programs.hix = {
      enable = lib.mkEnableOption "hix, the flake configuration front-end";

      package = lib.mkOption {
        type = lib.types.package;
        default = withSystem pkgs.stdenv.hostPlatform.system ({config, ...}: config.packages.default);
        defaultText = lib.literalExpression "hix.packages.\${system}.default";
        description = "The hix package.";
      };

      flake = lib.mkOption {
        type = lib.types.str;
        description = "Directory of the flake checkout hix builds from.";
      };

      platform = lib.mkOption {
        type = lib.types.enum [
          "nixos"
          "android"
        ];
        description = "What the configuration activates: a NixOS system or nix-on-droid.";
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
          Extra subcommands: `hix <name> [args]` runs `exec` with the
          arguments, and `hix help` and the completion list them.
        '';
      };

      postSwitch = lib.mkOption {
        type = lib.types.listOf hookType;
        default = [];
        description = ''
          Hooks `hix switch` and `hix upgrade` run after a successful
          activation, with the activated generation as the last argument
          and HIX_FLAKE set to the flake directory.
        '';
      };
    };

    config = lib.mkIf cfg.enable {
      home.packages = [cfg.package];
      xdg.configFile."hix/config.yaml".source = (pkgs.formats.yaml {}).generate "hix-config.yaml" {
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
