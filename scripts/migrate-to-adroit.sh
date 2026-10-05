#!/usr/bin/env bash
# Migrate live claude-squad state to Adroit's names.
#
#   ~/.claude-squad        -> ~/.adroit          (config, state.json, worktrees)
#   claudesquad_<session>  -> adroit_<session>   (tmux)
#   state.json worktree paths, and the git worktree admin links that record them
#
# Dry run by default. Pass --apply to execute.
set -euo pipefail

BACKUP="${ADROIT_MIGRATION_BACKUP:-${TMPDIR:-/tmp}/adroit-migration-$(date +%Y%m%d-%H%M%S)}"

OLD_DIR="$HOME/.claude-squad"
NEW_DIR="$HOME/.adroit"
OLD_PREFIX="claudesquad_"
NEW_PREFIX="adroit_"
PROVISION="$HOME/.local/bin/cs-provision"

APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1
# Only a real run leaves anything behind, a dry run included.
if (( APPLY )); then
  mkdir -p "$BACKUP"
  echo "backups -> $BACKUP"
  echo
fi
run() { if (( APPLY )); then echo "  + $*"; "$@"; else echo "  would: $*"; fi }

# Nothing may hold the state while it moves: the app rewrites state.json on quit,
# and it looks its tmux sessions up by the old name.
if pgrep -x cs >/dev/null || pgrep -x adroit >/dev/null; then
  echo "ERROR: cs/adroit is running (pid $( { pgrep -x cs; pgrep -x adroit; } | tr '\n' ' ')). Quit it first." >&2
  exit 1
fi

echo "== 1. back up state =="
if [[ -f "$OLD_DIR/state.json" ]]; then
  run cp -a "$OLD_DIR/state.json" "$BACKUP/state.json"
elif [[ -f "$NEW_DIR/state.json" ]]; then
  echo "  already migrated: $NEW_DIR/state.json exists"
fi

echo "== 2. rename tmux sessions =="
if tmux has-session 2>/dev/null || tmux list-sessions >/dev/null 2>&1; then
  while read -r s; do
    [[ -z "$s" ]] && continue
    run tmux rename-session -t "$s" "${NEW_PREFIX}${s#"$OLD_PREFIX"}"
  done < <(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep "^${OLD_PREFIX}" || true)
else
  echo "  no tmux server running; nothing to rename"
fi

echo "== 3. move the state directory =="
# Running the renamed binary before migrating creates a stub $NEW_DIR holding a
# freshly defaulted config, which would silently shadow the real one -- a default
# branch_prefix in place of yours is the kind of thing you notice three branches
# later. state.json is what the app actually tracks sessions in, so its absence
# means there is nothing here to lose. Moved aside rather than deleted.
if [[ -d "$NEW_DIR" && ! -f "$NEW_DIR/state.json" && -d "$OLD_DIR" ]]; then
  echo "  $NEW_DIR exists but tracks no sessions (no state.json) -- a stray invocation"
  run mv "$NEW_DIR" "$BACKUP/adroit-stub"
fi

if [[ -d "$OLD_DIR" && ! -e "$NEW_DIR" ]]; then
  run mv "$OLD_DIR" "$NEW_DIR"
elif [[ -d "$NEW_DIR" && -d "$OLD_DIR" ]]; then
  echo "  ERROR: both $OLD_DIR and $NEW_DIR hold real data. Resolve by hand." >&2
  exit 1
elif [[ -d "$NEW_DIR" ]]; then
  echo "  already migrated: $NEW_DIR exists and $OLD_DIR does not"
else
  echo "  $OLD_DIR does not exist; nothing to move"
fi

echo "== 4. rewrite worktree paths in state.json =="
if (( APPLY )); then
  python3 - "$NEW_DIR/state.json" "$OLD_DIR" "$NEW_DIR" <<'PY'
import json, sys, pathlib
path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
p = pathlib.Path(path)
if not p.exists():
    print("  no state.json; skipping"); raise SystemExit
d = json.loads(p.read_text())
n = 0
for inst in d.get('instances') or []:
    wt = inst.get('worktree') or {}
    for k, v in list(wt.items()):
        if isinstance(v, str) and v.startswith(old):
            wt[k] = new + v[len(old):]
            n += 1
p.write_text(json.dumps(d, indent=2))
print(f"  rewrote {n} path(s)")
PY
else
  echo "  would: rewrite worktree_path values ${OLD_DIR}/... -> ${NEW_DIR}/..."
fi

echo "== 5. repair the git worktree admin links =="
# git records the worktree path in <repo>/.git/worktrees/<name>/gitdir; `repair`
# re-links both directions from the new location, which is safer than editing it.
if (( APPLY )); then
  shopt -s nullglob
  for wt in "$NEW_DIR"/worktrees/*/; do
    wt="${wt%/}"
    repo=$(git -C "$wt" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)
    [[ -z "$repo" ]] && { echo "  ! $wt: cannot resolve its repo, skipping"; continue; }
    echo "  + git -C ${repo%/.git} worktree repair $wt"
    git -C "${repo%/.git}" worktree repair "$wt" || echo "  ! repair reported a problem for $wt"
  done
else
  echo "  would: git worktree repair each dir under $NEW_DIR/worktrees"
fi

echo "== 6. point cs-provision at the new directory =="
if [[ -f "$PROVISION" ]] && grep -q 'claude-squad' "$PROVISION"; then
  if (( APPLY )); then
    cp -a "$PROVISION" "$BACKUP/cs-provision"
    sed -i 's#\$HOME/\.claude-squad/worktrees#$HOME/.adroit/worktrees#g; s#claude-squad#adroit#g' "$PROVISION"
    echo "  + updated $PROVISION"
  else
    echo "  would: rewrite claude-squad -> adroit in $PROVISION"
    grep -n 'claude-squad' "$PROVISION" | sed 's/^/      /'
  fi
else
  echo "  nothing to change in $PROVISION"
fi

echo
(( APPLY )) && echo "DONE." || echo "DRY RUN — nothing changed. Re-run with --apply."
