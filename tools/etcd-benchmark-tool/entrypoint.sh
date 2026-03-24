#!/bin/bash
set -eo pipefail

# etcdctl wrapper with TLS args
ectl() {
  etcdctl \
    --endpoints=https://etcd-client:2379 \
    --cert=/etc/etcd/tls/client/etcd-client.crt \
    --key=/etc/etcd/tls/client/etcd-client.key \
    --cacert=/etc/etcd/tls/etcd-ca/ca.crt \
    "$@"
}

run_benchmark() {
  for i in $(seq 1 "$WORKERS"); do
    benchmark --endpoints=https://etcd-client:2379 \
      --cert=/etc/etcd/tls/client/etcd-client.crt \
      --key=/etc/etcd/tls/client/etcd-client.key \
      --cacert=/etc/etcd/tls/etcd-ca/ca.crt \
      --conns="$CLIENTS" --clients="$CLIENTS" \
      put --key-size="$KEY_SIZE" --val-size="$VAL_SIZE" \
      --key-space-size="$KEY_SPACE_SIZE" --sequential-keys \
      --total="$PUT_TOTAL" > /dev/null 2>&1 &
  done
  for i in $(seq 1 "${RANGE_WORKERS:-0}"); do
    benchmark --endpoints=https://etcd-client:2379 \
      --cert=/etc/etcd/tls/client/etcd-client.crt \
      --key=/etc/etcd/tls/client/etcd-client.key \
      --cacert=/etc/etcd/tls/etcd-ca/ca.crt \
      --conns="$CLIENTS" --clients="$CLIENTS" \
      range / --total="$RANGE_TOTAL" > /dev/null 2>&1 &
  done
  echo "$WORKERS put + ${RANGE_WORKERS:-0} range workers launched with $CLIENTS clients each"
  wait
}

run_cleanup() {
  echo "=== etcd cleanup: deleting benchmark keys ==="

  # Count Kubernetes keys first (safety check)
  k8s_count=$(ectl get --prefix /kubernetes.io/ \
    --count-only --write-out=fields --command-timeout=60s 2>/dev/null \
    | grep '"Count"' | awk '{print $NF}') || true

  total_before=$(ectl get "" --from-key \
    --count-only --write-out=fields --command-timeout=60s 2>/dev/null \
    | grep '"Count"' | awk '{print $NF}') || true

  # Fail closed: if either probe failed, refuse to delete unless forced
  if [ -z "$k8s_count" ] || [ -z "$total_before" ]; then
    if [ "${FORCE_CLEANUP:-false}" = "true" ]; then
      echo "WARNING: etcd probe failed but FORCE_CLEANUP=true, proceeding"
      k8s_count=${k8s_count:-0}
      total_before=${total_before:-0}
    else
      echo "ERROR: Failed to query etcd key counts (k8s_count='${k8s_count}', total='${total_before}')"
      echo "       Set FORCE_CLEANUP=true to override this safety check."
      return 1
    fi
  fi

  echo "Kubernetes keys (/kubernetes.io/): ${k8s_count}"

  benchmark_keys=$((total_before - k8s_count))
  echo "Total keys: ${total_before}, Benchmark keys to delete: ${benchmark_keys}"

  if [ "${benchmark_keys}" -le 0 ]; then
    echo "No benchmark keys to delete."
    return 0
  fi

  # Benchmark keys use binary.PutVarint (zigzag encoding), producing keys
  # whose first byte spans 0x00-0xFE across the full key space.
  #
  # Kubernetes keys start with "/" (0x2F). We delete everything EXCEPT
  # keys starting with "/" by using two range deletes:
  #
  #   Range 1: [0x01, "/")  → first bytes 0x01-0x2E (below "/")
  #   Range 2: ["0", 0xFF)  → first bytes 0x30-0xFE (above "/")
  #
  # This leaves a gap at exactly "/" (0x2F) where all Kubernetes keys live.
  # Keys with first byte 0x00 or 0xFF are skipped (tiny fraction, reclaimed
  # by compact+defrag).

  total_deleted=0

  # Range 1: keys with first byte 0x01-0x2E (below "/")
  echo "Deleting benchmark keys in range [\\x01, /)..."
  deleted=$(ectl del $'\x01' "/" --command-timeout=300s 2>/dev/null) || true
  deleted=$(echo "${deleted:-0}" | grep -o '[0-9]*' | head -1)
  deleted=${deleted:-0}
  total_deleted=$((total_deleted + deleted))
  echo "  Range [\\x01, /): deleted ${deleted} keys"

  # Range 2: keys with first byte 0x30-0xFE (above "/")
  # "0" = 0x30 in ASCII, which is one byte above "/" = 0x2F
  echo "Deleting benchmark keys in range [0, \\xff)..."
  deleted=$(ectl del "0" $'\xff' --command-timeout=300s 2>/dev/null) || true
  deleted=$(echo "${deleted:-0}" | grep -o '[0-9]*' | head -1)
  deleted=${deleted:-0}
  total_deleted=$((total_deleted + deleted))
  echo "  Range [0, \\xff): deleted ${deleted} keys"

  echo "Total deleted: ${total_deleted} keys"
  echo "(Note: keys with first byte \\x00 or \\xff may remain)"

  # Verify Kubernetes keys are intact
  k8s_after=$(ectl get --prefix /kubernetes.io/ \
    --count-only --write-out=fields --command-timeout=60s 2>/dev/null \
    | grep '"Count"' | awk '{print $NF}') || true
  echo "Kubernetes keys after cleanup: ${k8s_after:-0} (was: ${k8s_count:-0})"

  if [ "${k8s_after:-0}" -ne "${k8s_count:-0}" ]; then
    echo "WARNING: Kubernetes key count changed! Was ${k8s_count:-0}, now ${k8s_after:-0}"
  fi

  echo "=== cleanup complete ==="
}

case "${MODE:-benchmark}" in
  benchmark)
    run_benchmark
    ;;

  cleanup)
    run_cleanup
    ;;

  demo)
    DEMO_SLEEP="${DEMO_SLEEP_SECONDS:-600}"

    echo "=== DEMO MODE ==="
    echo "Phase 1: Generate benchmark data"
    run_benchmark

    echo ""
    echo "Phase 2: Sleeping ${DEMO_SLEEP}s for alerts to fire..."
    echo "         (alerts should appear in Cloud Monitoring / PagerDuty)"
    sleep "$DEMO_SLEEP"

    echo ""
    echo "Phase 3: Cleanup benchmark keys"
    run_cleanup

    echo ""
    echo "=== DEMO COMPLETE ==="
    echo "Run etcd-ops etcd-compact and remediate-etcd-pressure to resolve alerts."
    ;;

  *)
    echo "Unknown MODE: ${MODE}. Supported: benchmark, cleanup, demo"
    exit 1
    ;;
esac
