#!/bin/sh
# Builds battery-state from this source checkout and installs it for the current user
# (~/.local/bin, application menu). The installing is done by `battery-state --install`.
#
# Usage: ./install.sh            build and install into ~/.local
#        ./install.sh uninstall  remove it again
set -eu

cd "$(dirname "$0")"
export CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o battery-state ./src
if [ "${1:-}" = "uninstall" ]; then
    ./battery-state --uninstall
else
    ./battery-state --install
fi
