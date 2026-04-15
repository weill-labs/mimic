#!/usr/bin/env bash
# Detect new sync.Mutex, sync.RWMutex, or sync.Map usage in Go files.
# These indicate shared mutable state — use the actor model (channel-based
# event loop) instead.
#
# Scans only staged content when STAGED_ONLY=1 (set by the pre-commit hook).
# Otherwise scans the full working tree (CI / manual runs).
set -euo pipefail

# Known usages being tracked for conversion.
# Remove entries as they get refactored.
ALLOWLIST=(
  # Test helpers — coordination between test goroutines is acceptable
  "placeholder_for_test_helpers"
)

allowlist_pattern=$(printf "|%s" "${ALLOWLIST[@]}")
allowlist_pattern="${allowlist_pattern:1}"  # strip leading |

# Choose source: staged content (pre-commit) or working tree (CI / manual)
if [ "${STAGED_ONLY:-}" = "1" ]; then
  source_content=$(git diff --cached --diff-filter=ACM -U0 -- '*.go' \
    ':!vendor/' | grep -E '^\+' | grep -vF '+++' || true)
else
  source_content=$(grep -rn 'sync\.\(Mutex\|RWMutex\|Map\)\b' --include='*.go' \
    --exclude-dir=vendor \
    . 2>/dev/null || true)
fi

# No content to check — exit clean
if [ -z "$source_content" ]; then
  exit 0
fi

# Match sync.Mutex, sync.RWMutex, sync.Map declarations and field types
violations=$(
  echo "$source_content" |
  grep -E 'sync\.(Mutex|RWMutex|Map)\b' |
  grep -v '^\+?\s*//' |
  # Exclude allowlisted files
  grep -vE "(${allowlist_pattern})" || true
)

if [ -n "$violations" ]; then
  echo "ERROR: New sync.Mutex/RWMutex/Map usage detected."
  echo "Use the actor model (channel-based event loop) instead."
  echo ""
  echo "$violations"
  echo ""
  echo "If this is a legitimate exception, add the filename to scripts/lint-sync.sh ALLOWLIST."
  exit 1
fi
