# go.nix — this module's dependencies (FDR 0008); go.mod, gomod2nix.toml and
# the package graph are rendered or derived from it inside nix. Edit through
# the escape hatch (godyn-go) or by hand.
{
  flakeInputs = { };
  go = "1.26.1";
  module = "code.linenisgreat.com/conformist";
  replace = { };
  require = {
    "code.linenisgreat.com/hyphence/go" = {
      go = "1.26";
      hash = "sha256-HMeJOBTmoABNHhQho2KUazvLAhDvdWCba8ZvMtKiEv8=";
      version = "v0.4.0";
    };
    "code.linenisgreat.com/purse-first/libs/dewey" = {
      go = "1.26";
      hash = "sha256-WaAb5xVdkek2jtlcD8y+99VuZyAoCVzezyIrs5X9Mbg=";
      version = "v0.5.0";
    };
    "github.com/BurntSushi/toml" = {
      go = "1.18";
      hash = "sha256-ptdUJvuc21ixeLt+M5way/na3aCnCO4MYHWulWp8NEY=";
      version = "v1.6.0";
    };
    "github.com/adrg/xdg" = {
      go = "1.19";
      hash = "sha256-bo6tBgHS+3sl6f4oWpmdFrZjfV6eA/3xAlysSW0bIEs=";
      version = "v0.5.3";
    };
    "github.com/atotto/clipboard" = {
      hash = "sha256-ZZ7U5X0gWOu8zcjZcWbcpzGOGdycwq0TjTFh/eZHjXk=";
      indirect = true;
      version = "v0.1.4";
    };
    "github.com/aymanbagabas/go-osc52/v2" = {
      go = "1.16";
      hash = "sha256-6Bp0jBZ6npvsYcKZGHHIUSVSTAMEyieweAX2YAKDjjg=";
      indirect = true;
      version = "v2.0.1";
    };
    "github.com/catppuccin/go" = {
      go = "1.19";
      hash = "sha256-otcMhI62ezoKGqzG7Owi/NROep7O0voJxp6bwXYg9+Q=";
      indirect = true;
      version = "v0.3.0";
    };
    "github.com/charmbracelet/bubbles" = {
      go = "1.24.2";
      hash = "sha256-Vz9QgctlzJqggPwfi48Lbn38ZJXu3Y71byp5uuuzUvU=";
      indirect = true;
      version = "v1.0.0";
    };
    "github.com/charmbracelet/bubbletea" = {
      go = "1.24.0";
      hash = "sha256-7wr85TLszu1CHNEMv+o4w+r24Z0xdzCgecPv+ZtRX/A=";
      indirect = true;
      version = "v1.3.10";
    };
    "github.com/charmbracelet/colorprofile" = {
      go = "1.24.2";
      hash = "sha256-d/NjM/ybG+bGRRRMMcjbPCFGFS5noZRMaL05Ix5r/II=";
      indirect = true;
      version = "v0.4.1";
    };
    "github.com/charmbracelet/huh" = {
      go = "1.23.0";
      hash = "sha256-vDqcsW9uBPDt0FaOA7Bij+Q9CkozggstOZ0r557TaC4=";
      version = "v1.0.0";
    };
    "github.com/charmbracelet/lipgloss" = {
      go = "1.18";
      hash = "sha256-RHsRT2EZ1nDOElxAK+6/DC9XAaGVjDTgPvRh3pyCfY4=";
      indirect = true;
      version = "v1.1.0";
    };
    "github.com/charmbracelet/log" = {
      go = "1.19";
      hash = "sha256-3w1PCM/c4JvVEh2d0sMfv4C77Xs1bPa1Ea84zdynC7I=";
      version = "v0.4.2";
    };
    "github.com/charmbracelet/x/ansi" = {
      go = "1.24.2";
      hash = "sha256-UToZIkqXl9MEppcRgbeBqaaMeAzRkGa0w3lVUs6sxWI=";
      indirect = true;
      version = "v0.11.6";
    };
    "github.com/charmbracelet/x/cellbuf" = {
      go = "1.24.2";
      hash = "sha256-0S60XaWhKZG+TB3Kqe1oMn2Okwdq53nym8XayVSHHiM=";
      indirect = true;
      version = "v0.0.15";
    };
    "github.com/charmbracelet/x/exp/strings" = {
      go = "1.19";
      hash = "sha256-NWe8LHXUtrrABWFhmAzLNYAZyJIwN3C/T2OdaInjl9E=";
      indirect = true;
      version = "v0.0.0-20240722160745-212f7b056ed0";
    };
    "github.com/charmbracelet/x/term" = {
      go = "1.24.0";
      hash = "sha256-KF7IU1Luxl/sZP6XjomWB2e3lxSUS4/5AahhapGir/4=";
      indirect = true;
      version = "v0.2.2";
    };
    "github.com/clarete/langlang/go" = {
      go = "1.21";
      hash = "sha256-qUiwItAHjL89cSZ6gi+uo28FjPojin7pdg0IKtTuDI8=";
      version = "v0.0.12";
    };
    "github.com/clipperhouse/displaywidth" = {
      go = "1.18";
      hash = "sha256-9CNyTZPSncKQ7Y0my9DR4WYXDjtDHYNL512D691WDAM=";
      indirect = true;
      version = "v0.9.0";
    };
    "github.com/clipperhouse/stringish" = {
      go = "1.18";
      hash = "sha256-Mp8M1CRbwr6dcJ4BD9tXD5I78ZgCFEm0GDxJv0GYReg=";
      indirect = true;
      version = "v0.1.1";
    };
    "github.com/clipperhouse/uax29/v2" = {
      go = "1.18";
      hash = "sha256-Men4JLhiuEtAx8ZSzId5ciRWhud68o3k/B48ppwyxkM=";
      indirect = true;
      version = "v2.5.0";
    };
    "github.com/cpuguy83/go-md2man/v2" = {
      go = "1.12";
      hash = "sha256-wJnHgp+NPchXkR71ARLMjo4VryzgGkz2tYWPsC+3eFo=";
      indirect = true;
      version = "v2.0.6";
    };
    "github.com/davecgh/go-spew" = {
      hash = "sha256-fV9oI51xjHdOmEx6+dlq7Ku2Ag+m/bmbzPo6A4Y74qc=";
      indirect = true;
      version = "v1.1.2-0.20180830191138-d8f796af33cc";
    };
    "github.com/dustin/go-humanize" = {
      go = "1.16";
      hash = "sha256-yuvxYYngpfVkUg9yAmG99IUVmADTQA0tMbBXe0Fq0Mc=";
      indirect = true;
      version = "v1.0.1";
    };
    "github.com/erikgeiser/coninput" = {
      go = "1.16";
      hash = "sha256-OWSqN1+IoL73rWXWdbbcahZu8n2al90Y3eT5Z0vgHvU=";
      indirect = true;
      version = "v0.0.0-20211004153227-1c3628e74d0f";
    };
    "github.com/fsnotify/fsnotify" = {
      go = "1.17";
      hash = "sha256-WtpE1N6dpHwEvIub7Xp/CrWm0fd6PX7MKA4PV44rp2g=";
      indirect = true;
      version = "v1.9.0";
    };
    "github.com/go-logfmt/logfmt" = {
      go = "1.17";
      hash = "sha256-RtIG2qARd5sT10WQ7F3LR8YJhS8exs+KiuUiVf75bWg=";
      indirect = true;
      version = "v0.6.0";
    };
    "github.com/go-viper/mapstructure/v2" = {
      go = "1.18";
      hash = "sha256-lLfcV9z4n94hDhgyXJlde4bFB0hfzlbh+polqcJCwGE=";
      indirect = true;
      version = "v2.4.0";
    };
    "github.com/gobwas/glob" = {
      hash = "sha256-hYHMUdwxVkMOjSKjR7UWO0D0juHdI4wL8JEy5plu/Jc=";
      version = "v0.2.3";
    };
    "github.com/google/go-cmp" = {
      go = "1.21";
      hash = "sha256-JbxZFBFGCh/Rj5XZ1vG94V2x7c18L8XKB0N9ZD5F2rM=";
      indirect = true;
      version = "v0.7.0";
    };
    "github.com/google/shlex" = {
      go = "1.13";
      hash = "sha256-1f392pCmS7AXVKXIC1SvKlYtK/rvW47F5CCkGT2G6JM=";
      version = "v0.0.0-20191202100458-e7afc7fbc510";
    };
    "github.com/inconshreveable/mousetrap" = {
      go = "1.18";
      hash = "sha256-XWlYH0c8IcxAwQTnIi6WYqq44nOKUylSWxWO/vi+8pE=";
      indirect = true;
      version = "v1.1.0";
    };
    "github.com/itchyny/gojq" = {
      go = "1.24.0";
      hash = "sha256-egaBNHKKzwDwaUN4GT+Xvt11Nz6ojNMSIrXbcEisyI4=";
      version = "v0.12.19";
    };
    "github.com/itchyny/timefmt-go" = {
      go = "1.24";
      hash = "sha256-6h57JsmWju1QAx+mI9HZ+XceAeRnfLcA5HMWCHdymYE=";
      indirect = true;
      version = "v0.1.8";
    };
    "github.com/lucasb-eyer/go-colorful" = {
      go = "1.12";
      hash = "sha256-6BKrJsfmxie+YFAWzTYVPQfrwjQEXRo+J8LY+50C1BU=";
      indirect = true;
      version = "v1.3.0";
    };
    "github.com/mattn/go-isatty" = {
      go = "1.15";
      hash = "sha256-qhw9hWtU5wnyFyuMbKx+7RB8ckQaFQ8D+8GKPkN3HHQ=";
      indirect = true;
      version = "v0.0.20";
    };
    "github.com/mattn/go-localereader" = {
      hash = "sha256-JlWckeGaWG+bXK8l8WEdZqmSiTwCA8b1qbmBKa/Fj3E=";
      indirect = true;
      version = "v0.0.1";
    };
    "github.com/mattn/go-runewidth" = {
      go = "1.20";
      hash = "sha256-GpnbKplhX410Q/eIdknvWbYZgdav1keN+7wNUeOSMHE=";
      indirect = true;
      version = "v0.0.19";
    };
    "github.com/mitchellh/hashstructure/v2" = {
      go = "1.14";
      hash = "sha256-O4Yw4pPQECWe8DoVDIH2nUMN8Zl8waS7/O1sv18M2Xs=";
      indirect = true;
      version = "v2.0.2";
    };
    "github.com/muesli/ansi" = {
      go = "1.17";
      hash = "sha256-qRKn0Bh2yvP0QxeEMeZe11Vz0BPFIkVcleKsPeybKMs=";
      indirect = true;
      version = "v0.0.0-20230316100256-276c6243b2f6";
    };
    "github.com/muesli/cancelreader" = {
      go = "1.17";
      hash = "sha256-uEPpzwRJBJsQWBw6M71FDfgJuR7n55d/7IV8MO+rpwQ=";
      indirect = true;
      version = "v0.2.2";
    };
    "github.com/muesli/termenv" = {
      go = "1.17";
      hash = "sha256-hGo275DJlyLtcifSLpWnk8jardOksdeX9lH4lBeE3gI=";
      indirect = true;
      version = "v0.16.0";
    };
    "github.com/otiai10/copy" = {
      go = "1.18";
      hash = "sha256-8RR7u17SbYg9AeBXVHIv5ZMU+kHmOcx0rLUKyz6YtU0=";
      version = "v1.14.1";
    };
    "github.com/otiai10/mint" = {
      go = "1.18";
      hash = "sha256-/FT3dYP2+UiW/qe1pxQ7HiS8et4+KHGPIMhc+8mHvzw=";
      indirect = true;
      version = "v1.6.3";
    };
    "github.com/pelletier/go-toml/v2" = {
      go = "1.21.0";
      hash = "sha256-8qQIPldbsS5RO8v/FW/se3ZsAyvLzexiivzJCbGRg2Q=";
      indirect = true;
      version = "v2.2.4";
    };
    "github.com/pmezard/go-difflib" = {
      hash = "sha256-XA4Oj1gdmdV/F/+8kMI+DBxKPthZ768hbKsO3d9Gx90=";
      indirect = true;
      version = "v1.0.1-0.20181226105442-5d4384ee4fb2";
    };
    "github.com/rivo/uniseg" = {
      go = "1.18";
      hash = "sha256-rDcdNYH6ZD8KouyyiZCUEy8JrjOQoAkxHBhugrfHjFo=";
      indirect = true;
      version = "v0.4.7";
    };
    "github.com/russross/blackfriday/v2" = {
      hash = "sha256-R+84l1si8az5yDqd5CYcFrTyNZ1eSYlpXKq6nFt4OTQ=";
      indirect = true;
      version = "v2.1.0";
    };
    "github.com/sagikazarmark/locafero" = {
      go = "1.23.0";
      hash = "sha256-PUX8dzJtkD8YDZFNqpHnl4qgb0tE1W/DLnL7V+/d1z4=";
      indirect = true;
      version = "v0.11.0";
    };
    "github.com/sourcegraph/conc" = {
      go = "1.20";
      hash = "sha256-AUNFlY6K7s1aoW/vb4pjK84ROdnVZY1i6cOmdeG+wN8=";
      indirect = true;
      version = "v0.3.1-0.20240121214520-5f936abd7ae8";
    };
    "github.com/spf13/afero" = {
      go = "1.23.0";
      hash = "sha256-LhcezbOqfuBzacytbqck0hNUxi6NbWNhifUc5/9uHQ8=";
      indirect = true;
      version = "v1.15.0";
    };
    "github.com/spf13/cast" = {
      go = "1.21.0";
      hash = "sha256-dQ6Qqf26IZsa6XsGKP7GDuCj+WmSsBmkBwGTDfue/rk=";
      indirect = true;
      version = "v1.10.0";
    };
    "github.com/spf13/cobra" = {
      go = "1.15";
      hash = "sha256-nbRCTFiDCC2jKK7AHi79n7urYCMP5yDZnWtNVJrDi+k=";
      version = "v1.10.2";
    };
    "github.com/spf13/pflag" = {
      go = "1.12";
      hash = "sha256-uDPnWjHpSrzXr17KEYEA1yAbizfcsfo5AyztY2tS6ZU=";
      version = "v1.0.10";
    };
    "github.com/spf13/viper" = {
      go = "1.23.0";
      hash = "sha256-A9A8i7HH/ge4j3hw7G++HNj8BjhhpZKvxHhfY+QAxkI=";
      version = "v1.21.0";
    };
    "github.com/stretchr/testify" = {
      go = "1.17";
      hash = "sha256-sWfjkuKJyDllDEtnM8sb/pdLzPQmUYWYtmeWz/5suUc=";
      version = "v1.11.1";
    };
    "github.com/subosito/gotenv" = {
      go = "1.18";
      hash = "sha256-LspbjTniiq2xAICSXmgqP7carwlNaLqnCTQfw2pa80A=";
      indirect = true;
      version = "v1.6.0";
    };
    "github.com/xo/terminfo" = {
      go = "1.19";
      hash = "sha256-GyCDxxMQhXA3Pi/TsWXpA8cX5akEoZV7CFx4RO3rARU=";
      indirect = true;
      version = "v0.0.0-20220910002029-abceb7e1c41e";
    };
    "go.etcd.io/bbolt" = {
      go = "1.23";
      hash = "sha256-ahFku15afu8YNTOAFR7v/MmKyxKhvv+ZwqrgP5lguNQ=";
      version = "v1.4.3";
    };
    "go.yaml.in/yaml/v3" = {
      go = "1.16";
      hash = "sha256-NkGFiDPoCxbr3LFsI6OCygjjkY0rdmg5ggvVVwpyDQ4=";
      indirect = true;
      version = "v3.0.4";
    };
    "golang.org/x/crypto" = {
      go = "1.26.0";
      hash = "sha256-3/hkB+mZVBKnoxUaJv7DOzvxaL+8uQ7V6ofW4JiHhz4=";
      version = "v0.57.0";
    };
    "golang.org/x/exp" = {
      go = "1.25.0";
      hash = "sha256-JaDJGLIRoJjjvsg3dgfFuo7XApEJO2V4kUDmd58qTLI=";
      indirect = true;
      version = "v0.0.0-20260410095643-746e56fc9e2f";
    };
    "golang.org/x/sync" = {
      go = "1.26.0";
      hash = "sha256-nF8YZFVC+TitjkderlqgGwJkuMqJCxI+fIElVLjBT2M=";
      version = "v0.23.0";
    };
    "golang.org/x/sys" = {
      go = "1.26.0";
      hash = "sha256-u+LwI76YIeg1asQKZItD2mnL4Yvx8veLhBuL8wv60Ls=";
      version = "v0.48.0";
    };
    "golang.org/x/term" = {
      go = "1.26.0";
      hash = "sha256-w/LexmQ91G8N1zdjIujuaEIONVfAg7U/DciHVYp0+QY=";
      version = "v0.46.0";
    };
    "golang.org/x/text" = {
      go = "1.26.0";
      hash = "sha256-bviKbNmkN4Yf2dpgwmP6yNcZEakCcv1R+YyAxCdJGkY=";
      indirect = true;
      version = "v0.42.0";
    };
    "golang.org/x/xerrors" = {
      go = "1.18";
      hash = "sha256-bE7CcrnAvryNvM26ieJGXqbAtuLwHaGcmtVMsVnksqo=";
      indirect = true;
      version = "v0.0.0-20240903120638-7835f813f4da";
    };
    "gopkg.in/check.v1" = {
      go = "1.11";
      hash = "sha256-VlIpM2r/OD+kkyItn6vW35dyc0rtkJufA93rjFyzncs=";
      indirect = true;
      version = "v1.0.0-20201130134442-10cb98267c6c";
    };
    "gopkg.in/yaml.v3" = {
      hash = "sha256-FqL9TKYJ0XkNwJFnq9j0VvJ5ZUU1RvH/52h/f5bkYAU=";
      indirect = true;
      version = "v3.0.1";
    };
    "mvdan.cc/sh/v3" = {
      go = "1.25.0";
      hash = "sha256-FM+2xpGELZLm4Gc09OE5Z3JeNLPMvzeb1wRcdAIeKy4=";
      version = "v3.13.1";
    };
  };
}
