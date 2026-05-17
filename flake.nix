{
  description = "Igloo server package and OCI container image";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];

      forAllSystems = nixpkgs.lib.genAttrs systems;
      goSourceVersion = "1.26.3";
      goSourceHash = "sha256-HGRoddCqh5kTMYTtV895/yS97+jIggRwYCqdPW2Rkrg=";
      goFor =
        pkgs:
        (pkgs.go_1_26 or pkgs.go).overrideAttrs (_: {
          version = goSourceVersion;
          src = pkgs.fetchurl {
            url = "https://go.dev/dl/go${goSourceVersion}.src.tar.gz";
            hash = goSourceHash;
          };
        });
      patchedRuntimeOverlay =
        final: prev:
        let
          ffmpeg81 = {
            version = "8.1";
            source = builtins.fetchurl {
              url = "https://ffmpeg.org/releases/ffmpeg-8.1.tar.xz";
              sha256 = "sha256-sHKu1ocZmMzps253dAMxBcop4zYyvltjR/MgaJjgdWo=";
            };
          };
          openssl362 = prev.openssl_3_6.overrideAttrs (old: {
            version = "3.6.2";
            src = builtins.fetchurl {
              url = "https://github.com/openssl/openssl/releases/download/openssl-3.6.2/openssl-3.6.2.tar.gz";
              sha256 = "sha256-qvUaH+BkOE+BHa6utOxNznNA7IvYkwJ+7mdq8x6DoE8=";
            };
            patches = builtins.filter (
              patch:
              !(prev.lib.hasInfix "openssl-aes-gcm-ppc-remove-localentry-directive" (toString patch))
            ) (old.patches or [ ]);
          });
        in
        {
          openssl = openssl362;
          openssl_3_6 = openssl362;

          ffmpeg_8 = prev.ffmpeg_8.override ffmpeg81;
          ffmpeg_8-headless = prev.ffmpeg_8-headless.override ffmpeg81;
          ffmpeg_8-full = prev.ffmpeg_8-full.override ffmpeg81;
          ffmpeg = final.ffmpeg_8;
          ffmpeg-headless = final.ffmpeg_8-headless;
          ffmpeg-full = final.ffmpeg_8-full;
        };
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
          };
          runtimePkgs = pkgs.extend patchedRuntimeOverlay;
          lib = pkgs.lib;
          revision = self.shortRev or (self.dirtyShortRev or "dev");
          containerImageName = "ghcr.io/screwys/igloo";
          go = goFor pkgs;
          buildGoModule = pkgs.buildGoModule.override { inherit go; };
          pythonPackages = runtimePkgs.python3Packages;
          runtimeRequirementLines = lib.splitString "\n" (builtins.readFile ./requirements-runtime.txt);
          runtimeToolVersion =
            package:
            let
              prefix = "${package}==";
              matches = builtins.filter (line: lib.hasPrefix prefix line) runtimeRequirementLines;
            in
            if builtins.length matches != 1 then
              throw "expected exactly one ${package} pin in requirements-runtime.txt"
            else
              lib.removePrefix prefix (builtins.head matches);

          ytDlp = pythonPackages.buildPythonApplication rec {
            pname = "yt-dlp";
            version = runtimeToolVersion "yt-dlp";
            pyproject = true;

            src = runtimePkgs.fetchPypi {
              pname = "yt_dlp";
              inherit version;
              hash = "sha256-unqjHVM/H/zP5w5CFZbXyo/wvxOY3Gu2WLfZ3sBX0sk=";
            };

            build-system = [
              pythonPackages.hatchling
            ];

            dependencies = [
              pythonPackages.requests
            ];

            doCheck = false;
            pythonImportsCheck = [ "yt_dlp" ];

            meta = {
              description = "Command-line program to download videos";
              homepage = "https://github.com/yt-dlp/yt-dlp";
              license = lib.licenses.unlicense;
              mainProgram = "yt-dlp";
              platforms = lib.platforms.linux;
            };
          };

          galleryDl = pythonPackages.buildPythonApplication rec {
            pname = "gallery_dl";
            version = runtimeToolVersion "gallery-dl";
            pyproject = true;

            src = runtimePkgs.fetchPypi {
              inherit pname version;
              hash = "sha256-tZ8cO1h4PJyQTTi6JMtk4gBDQchBAJA1ZJEzQPuXdn8=";
            };

            build-system = [
              pythonPackages.setuptools
            ];

            dependencies = [
              pythonPackages.requests
            ];

            doCheck = false;
            pythonImportsCheck = [ "gallery_dl" ];

            meta = {
              description = "Command-line program to download image galleries";
              homepage = "https://github.com/mikf/gallery-dl";
              license = lib.licenses.gpl2Only;
              mainProgram = "gallery-dl";
              platforms = lib.platforms.linux;
            };
          };

          sourceRoots = [
            "cmd"
            "internal"
            "locales"
            "static"
          ];

          source = lib.cleanSourceWith {
            src = ./.;
            filter =
              path: _type:
              let
                root = toString ./.;
                rel = lib.removePrefix (root + "/") (toString path);
              in
              rel == ""
              || rel == "go.mod"
              || rel == "go.sum"
              || lib.any (prefix: rel == prefix || lib.hasPrefix "${prefix}/" rel) sourceRoots;
          };

          igloo = buildGoModule {
            pname = "igloo";
            version = "0.0.0-${revision}";

            src = source;
            vendorHash = "sha256-UrQBhjfoXgX40L2L4JgMoBF479ncAW7v/5hQBoZRTxA=";

            subPackages = [
              "cmd/igloo"
              "cmd/adduser"
            ];

            ldflags = [
              "-s"
              "-w"
            ];

            postBuild = ''
              go run ./cmd/igloo-assets
            '';

            overrideModAttrs = _: {
              postBuild = "";
            };

            postInstall = ''
              mv "$out/bin/adduser" "$out/bin/igloo-adduser"
              mkdir -p "$out/share/igloo"
              cp -R static locales "$out/share/igloo/"
            '';

            doCheck = false;

            meta = {
              description = "Local-first video archive server";
              homepage = "https://github.com/screwys/igloo";
              license = lib.licenses.gpl3Plus;
              mainProgram = "igloo";
              platforms = lib.platforms.linux;
            };
          };

          runtimeEnv = runtimePkgs.buildEnv {
            name = "igloo-runtime";
            paths = [
              igloo
              runtimePkgs.cacert
              (lib.getBin runtimePkgs.ffmpeg-headless)
              galleryDl
              ytDlp
            ];
            pathsToLink = [
              "/bin"
              "/etc"
              "/share"
            ];
          };

          container = pkgs.dockerTools.buildLayeredImage {
            name = containerImageName;
            tag = "latest";
            maxLayers = 120;

            contents = [
              runtimeEnv
              pkgs.dockerTools.fakeNss
            ];

            extraCommands = ''
              mkdir -p app usr/local/bin igloo/data igloo/config tmp
              chmod 1777 tmp

              ln -s ${igloo}/share/igloo/static app/static
              ln -s ${igloo}/share/igloo/locales app/locales
              ln -s ${igloo}/bin/igloo usr/local/bin/igloo
              ln -s ${igloo}/bin/igloo-adduser usr/local/bin/igloo-adduser
            '';

            fakeRootCommands = ''
              chown -R 10001:10001 igloo
            '';

            config = {
              Cmd = [ "/usr/local/bin/igloo" ];
              Env = [
                "PATH=/usr/local/bin:${runtimeEnv}/bin:/bin"
                "SSL_CERT_FILE=${runtimePkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
                "REQUESTS_CA_BUNDLE=${runtimePkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
                "LANG=C.UTF-8"
                "HOME=/tmp"
                "IGLOO_DATA_DIR=/igloo/data"
                "IGLOO_CONFIG_DIR=/igloo/config"
                "IGLOO_REPO_DIR=/app"
                "IGLOO_PORT=5001"
                "IGLOO_ENABLED_PLATFORMS=all"
              ];
              ExposedPorts = {
                "5001/tcp" = { };
              };
              Volumes = {
                "/igloo" = { };
              };
              User = "10001:10001";
              WorkingDir = "/app";
            };
          };
        in
        {
          default = igloo;
          inherit container igloo;
          gallery-dl = galleryDl;
          yt-dlp = ytDlp;
        }
      );

      apps = forAllSystems (
        system:
        let
          pkg = self.packages.${system}.igloo;
        in
        {
          default = self.apps.${system}.igloo;
          igloo = {
            type = "app";
            program = "${pkg}/bin/igloo";
          };
        }
      );

      checks = forAllSystems (system: {
        default = self.packages.${system}.igloo;
        container = self.packages.${system}.container;
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          go = goFor pkgs;
        in
        {
          default = pkgs.mkShell {
            packages = [
              go
              (pkgs.lib.getBin pkgs.ffmpeg-headless)
              self.packages.${system}.gallery-dl
              self.packages.${system}.yt-dlp
            ];
          };
        }
      );
    };
}
