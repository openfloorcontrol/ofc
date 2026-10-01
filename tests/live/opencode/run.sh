#!/usr/bin/env bash
# Live ACP checks against opencode, run through mf with a real model.
#
#   tests/live/opencode/run.sh [mf-model]     (default: groq/qwen38)
#
# Needs ofc (or $OFC), mf and opencode. Each check uses its own sessions
# directory under a fresh temp dir, which is kept for inspection.
#
#   1. one-shot   — a single turn gets an answer
#   2. sessions   — two web sessions get separate opencode processes and
#                   don't see each other's conversation
#   3. resume     — a second `ofc run --session` continues the agent's
#                   session in a new process
set -euo pipefail

MODEL=${1:-groq/qwen38}
OFC=${OFC:-ofc}
PORT=${PORT:-18199}
BP="$(cd "$(dirname "$0")" && pwd)/blueprint.yaml"
WORK=$(mktemp -d -t ofc-live-opencode.XXXXXX)
export OFC_LIVE_MODEL=$MODEL

failures=0
pass() { echo "  PASS $*"; }
fail() { echo "  FAIL $*"; failures=$((failures + 1)); }

# reply prints @oc's last message from ofc's --json output on stdin.
reply() { grep '"from":"@oc"' | tail -1 | sed 's/.*"content":"\([^"]*\)".*/\1/'; }

echo "model=$MODEL work=$WORK"

echo "1. one-shot"
export OFC_SESSIONS_DIR=$WORK/oneshot
r=$("$OFC" run -f "$BP" --json "What is 2+2? Answer with the number only." 2>"$WORK/oneshot.err" | reply)
[[ $r == *4* ]] && pass "answered: $r" || fail "answer was: '$r' (see $WORK/oneshot.err)"

echo "2. sessions"
export OFC_SESSIONS_DIR=$WORK/sessions
"$OFC" run -f "$BP" --web --port "$PORT" </dev/null >"$WORK/web.log" 2>&1 &
web=$!
trap 'kill $web 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do grep -q 'token=' "$WORK/web.log" && break; sleep 1; done
tok=$(grep -o 'token=[0-9a-f]*' "$WORK/web.log" | head -1 | cut -d= -f2)
api=http://127.0.0.1:$PORT/api/v1/sessions
auth="Authorization: Bearer $tok"

new_session() { curl -sf -X POST -H "$auth" "$api" | grep -o '[0-9a-f-]\{36\}'; }
post() { curl -sf -o /dev/null -X POST -H "$auth" -H 'Content-Type: application/json' -d "{\"content\":\"$2\"}" "$api/$1/messages"; }
# wait_replies waits until session $1 has $2 replies from @oc.
wait_replies() {
  for _ in $(seq 1 120); do
    [[ $(curl -sf -H "$auth" "$api/$1/messages" | grep -o '"from":"@oc"' | wc -l) -ge $2 ]] && return 0
    sleep 1
  done
  return 1
}
last_reply() { curl -sf -H "$auth" "$api/$1/messages" | grep -o '"from":"@oc","content":"[^"]*"' | tail -1 | sed 's/.*"content":"//; s/"$//'; }

a=$(new_session); b=$(new_session)
post "$a" "Remember the secret word APPLE. Reply with just: noted."
post "$b" "Remember the secret word BANANA. Reply with just: noted."
wait_replies "$a" 1 && wait_replies "$b" 1 || fail "no first replies"
procs=$(ps -o args= --ppid "$web" | grep -c '^opencode' || true)
[[ $procs == 2 ]] && pass "two sessions, two opencode processes" || fail "$procs opencode processes, want 2"
post "$a" "What is the secret word? Answer with the word only."
post "$b" "What is the secret word? Answer with the word only."
wait_replies "$a" 2 && wait_replies "$b" 2 || fail "no second replies"
ra=$(last_reply "$a"); rb=$(last_reply "$b")
[[ $ra == *APPLE* && $ra != *BANANA* ]] && pass "session A: $ra" || fail "session A answered: '$ra'"
[[ $rb == *BANANA* && $rb != *APPLE* ]] && pass "session B: $rb" || fail "session B answered: '$rb'"
kill "$web"; wait "$web" 2>/dev/null || true
trap - EXIT

echo "3. resume"
export OFC_SESSIONS_DIR=$WORK/resume
"$OFC" run -f "$BP" --json "Remember the secret word KIWI. Reply with just: noted." >/dev/null 2>"$WORK/resume1.err"
sid=$(ls "$OFC_SESSIONS_DIR" | head -1); sid=${sid%.jsonl}
r=$("$OFC" run -f "$BP" --json --session "$sid" "What was the secret word? Answer with the word only." 2>"$WORK/resume2.err" | reply)
[[ $r == *KIWI* ]] && pass "resumed session recalled: $r" || fail "resumed session answered: '$r'"
acp=$(grep -o '"acp_session_id":"[^"]*"' "$OFC_SESSIONS_DIR/$sid.jsonl" | sort -u | wc -l)
[[ $acp == 1 ]] && pass "one ACP session across both runs" || fail "$acp different ACP session ids recorded"

echo
if [[ $failures == 0 ]]; then echo "all live checks passed"; else echo "$failures live check(s) failed"; exit 1; fi
