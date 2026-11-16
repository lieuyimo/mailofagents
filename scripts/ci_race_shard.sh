#!/usr/bin/env bash
# ci_race_shard.sh — functional shards for the server race-detector job.
#
# The server package's end-to-end tests spend most of their time in real
# waits (timers, rate-limit windows, push aggregation); -race instrumentation
# multiplies that, making one job the CI critical path. Split the package by
# feature so several jobs run in parallel.
#
# Usage:
#   ci_race_shard.sh auth|mail|org|ops   # print the -run regex for that shard
#   ci_race_shard.sh check               # every server test belongs to exactly one shard
set -u

pattern_for() {
  case "$1" in
    auth) echo '^(TestAdmin|TestAvatar|TestClientIPXFF|TestNames|TestOnboarding|TestPassword|TestProfile|TestPublic|TestRegisterPasswordless|TestRegLimiter|TestTokenAuth)' ;;
    mail) echo '^(TestAttachment|TestInbox|TestRecipientLimits|TestSearch|TestSentStatus|TestThread|TestWhitelist)' ;;
    org)  echo '^(TestBoard|TestLivenessPull|TestPrefsLiveness|TestRegisterSubordinate|TestSub)' ;;
    ops)  echo '^(TestLegacyPushSeed|TestOptions|TestPush|TestSelfDescribe|TestSiteCopy|TestStaticCache|TestUpdates|TestVAPID|TestWorker)' ;;
    *) return 2 ;;
  esac
}

case "${1:-}" in
  check)
    tests=$(go test -list '^Test' ./internal/server | grep '^Test' || true)
    fail=0
    for t in $tests; do
      n=0
      for s in auth mail org ops; do
        re=$(pattern_for "$s")
        if [[ $t =~ $re ]]; then n=$((n + 1)); fi
      done
      if [ "$n" -ne 1 ]; then
        echo "UNASSIGNED OR AMBIGUOUS: $t (matches $n shards)"
        fail=1
      fi
    done
    if [ "$fail" -eq 0 ]; then
      echo "RACE SHARDS: every server test is assigned to exactly one shard"
    fi
    exit "$fail"
    ;;
  auth | mail | org | ops) pattern_for "$1" ;;
  *)
    echo "usage: $0 {auth|mail|org|ops|check}" >&2
    exit 2
    ;;
esac
