#!/usr/bin/env bash
# prasar as a tmux plugin: TPM entry point (also works with a plain
# `run-shell` on this file). Binds prefix + @prasar-key to open prasar
# (write a post for X and LinkedIn) in a popup; the same key inside the
# popup closes it, keeping what you wrote as a draft.
#
# Options (set them before TPM's `run` line):
#   @prasar-key       key after the prefix (default B; "none" binds nothing)
#   @prasar-width     popup width  (default 90%)
#   @prasar-height    popup height (default 88%)
#   @prasar-title     popup title  (default " prasar ")
#   @prasar-launcher  your own popup command instead of display-popup: it is
#                     run with display-popup's arguments appended (-E -d DIR
#                     -T TITLE CMD), tmux formats expanded, e.g.
#                     "~/.tmux/scripts/popup.sh '#{client_name}'"

dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
run="$dir/scripts/prasar-run"

opt() { local v; v=$(tmux show-option -gqv "$1"); printf '%s' "${v:-$2}"; }
key=$(opt @prasar-key B)
[ "$key" = none ] && exit 0
width=$(opt @prasar-width 90%)
height=$(opt @prasar-height 88%)
title=$(opt @prasar-title ' prasar ')
launcher=$(opt @prasar-launcher '')

cmd="'$run' --popup '$key'"
if [ -n "$launcher" ]; then
  open="run -b \"$launcher -E -d '#{pane_current_path}' -T '$title' $cmd\""
else
  open="display-popup -E -w '$width' -h '$height' -d '#{pane_current_path}' -T '$title' \"$cmd\""
fi

tmux bind-key -N "Open prasar post popup (write once for X and LinkedIn; prefix $key again closes)" "$key" "$open"
