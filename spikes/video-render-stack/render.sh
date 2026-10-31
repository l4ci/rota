#!/bin/bash
# usage: render.sh Comp out conc
cd "$(dirname "$0")/rem"
free -m | sed -n 2p | sed 's/^/before: /'
( while true; do free -m | sed -n 2p | awk '{print $3}' >> ../memsamples-$(basename $2).txt; sleep 0.5; done ) & S=$!
/usr/bin/time -v -o ../time-$(basename $2).txt npx remotion render src/index.ts $1 $2 --concurrency $3 > ../render-$(basename $2).log 2>&1
kill $S
free -m | sed -n 2p | sed 's/^/after: /'
grep -E "Elapsed|Maximum resident" ../time-$(basename $2).txt
echo "peak system used MB: $(sort -n ../memsamples-$(basename $2).txt | tail -1)"
