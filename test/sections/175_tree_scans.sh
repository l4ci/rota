# Keep the fast-tier source-guard wiring covered at the full merge gate too.
echo "Section 175: fast-tier tree-scan wiring"
python3 "$REPO/test/fast_tree_scans_test.py" || fail "fast-tier tree-scan wiring"
pass "fast-tier tree-scan wiring"
