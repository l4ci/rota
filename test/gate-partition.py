"""Deal the smoke sections to shards for test/gate.sh and print the shard count.

Usage: gate-partition.py <repo> <shards> <random 0|1> <logs-dir>

Greedy, heaviest first, by the measured seconds in test/shard-weights.txt
(unlisted sections count 1). Random ignores the weights and deals a shuffle
round-robin. Either way a shard runs its sections in serial order: only which
shard owns a section changes. Writes <logs-dir>/shard<N>.list per shard.
"""
import os
import random
import sys

repo, n, rnd, logs = sys.argv[1], int(sys.argv[2]), sys.argv[3] == "1", sys.argv[4]
secs = sorted(os.path.join("test/sections", f) for f in os.listdir(os.path.join(repo, "test/sections")) if f.endswith(".sh"))
w = {}
try:
    for line in open(os.path.join(repo, "test/shard-weights.txt")):
        p = line.split()
        if len(p) == 2 and not line.startswith("#"):
            w[p[0]] = float(p[1])
except OSError:
    pass
n = max(1, min(n, len(secs)))
shards = [[] for _ in range(n)]
if rnd:
    random.shuffle(secs)
    for i, s in enumerate(secs):
        shards[i % n].append(s)
else:
    load = [0.0] * n
    for s in sorted(secs, key=lambda s: -w.get(os.path.basename(s), 1.0)):
        i = load.index(min(load))
        shards[i].append(s)
        load[i] += w.get(os.path.basename(s), 1.0)
shards = [sorted(sh) for sh in shards]
for i, sh in enumerate(shards):
    with open(os.path.join(logs, "shard%d.list" % (i + 1)), "w") as f:
        f.write("\n".join(sh) + "\n")
print(n)
