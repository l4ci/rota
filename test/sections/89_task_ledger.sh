echo "task ledger: resume a half-done plan from Task: trailers (#247)"

# The ledger is plain git: the documented read command must list exactly the
# finished tasks, ignore unmarked commits, and expand a same-file carve-out.
TL="$(mktemp -d "$TMP/task-ledger.XXXXXX")"
(
  cd "$TL" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && git checkout -q -b rota/feature \
    && for n in 1 2; do
         echo "$n" > "t$n"; git add "t$n"
         git -c user.email=a@b -c user.name=n commit -q -m "feat: task $n" -m "Task: M01-B07/$n"
       done \
    && echo w > wip && git add wip && git -c user.email=a@b -c user.name=n commit -q -m "wip: pause before context cutoff" \
    && echo 34 > t34 && git add t34 \
    && git -c user.email=a@b -c user.name=n commit -q -m "feat: tasks 3 and 4" -m "Task: M01-B07/3
Task: M01-B07/4"
) || fail "task ledger fixture setup failed"

LEDGER=$(cd "$TL" && git log --format='%(trailers:key=Task,valueonly,separator=%x0a)' main..HEAD | sed '/^$/d' | sort)
[ "$LEDGER" = "$(printf 'M01-B07/1\nM01-B07/2\nM01-B07/3\nM01-B07/4')" ] || fail "ledger read wrong: $LEDGER"
pass "the ledger read lists every marked task and skips the unmarked wip commit"

# Half-done plan: only tasks 1-2 landed, so 3-4 remain.
HALF=$(cd "$TL" && git log --format='%(trailers:key=Task,valueonly,separator=%x0a)' main..HEAD~1 | sed '/^$/d' | sort | tr '\n' ' ')
[ "$HALF" = "M01-B07/1 M01-B07/2 " ] || fail "half-done ledger wrong: $HALF"
REMAIN=""
for n in 1 2 3 4; do
  case " $HALF" in *" M01-B07/$n "*) ;; *) REMAIN="$REMAIN$n " ;; esac
done
[ "$REMAIN" = "3 4 " ] || fail "remaining tasks should be 3 4, got: $REMAIN"
pass "a half-done plan resumes at the first unmarked task"

# The skills must keep naming the marker and the shared reference.
for f in skills/rota-work/SKILL.md skills/rota-pause/SKILL.md; do
  grep -q 'task-ledger.md' "$REPO/$f" || fail "$f does not cite references/task-ledger.md"
done
grep -q 'Task: <key>/<N>' "$REPO/skills/rota-work/SKILL.md" || fail "rota-work Step 7.5 lost the Task trailer"
grep -q 'trailers:key=Task' "$REPO/skills/references/task-ledger.md" || fail "task-ledger.md lost the read command"
pass "rota-work and rota-pause cite the task ledger"
