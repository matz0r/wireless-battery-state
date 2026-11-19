#!/bin/sh
# Builds battery-state from this source checkout into target/release and installs it for
# the current user (~/.local/bin, icon, application menu). The installing is done by
# `battery-state --install`.
#
# Usage: ./install.sh            build and install into ~/.local
#        ./install.sh uninstall  remove it again
set -eu

cd "$(dirname "$0")"
make release
if [ "${1:-}" = "uninstall" ]; then
    target/release/battery-state --uninstall
else
    target/release/battery-state --install
fi
