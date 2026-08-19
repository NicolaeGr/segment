{
  pkgs,
  # lib,
  # config,
  # inputs,
  ...
}:
{
  packages = with pkgs; [
    sqlite
    templ
    tailwindcss_4
    gopls
  ];

  languages.go.enable = true;

  services.postgres.enable = true;
  services.redis.enable = true;

  enterShell = ''
    echo "Devenv environment loaded!"
    go version
  '';
}
