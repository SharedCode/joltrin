#!/usr/bin/env bash
# Fails if a YouTube video the site embeds is gone, private, or not embeddable,
# which shows visitors a black box. It asks YouTube's oEmbed endpoint about every
# embed id in the assembled site. A network error is retried and then only
# warned about, so YouTube being slow never blocks a pull request.
set -euo pipefail

SITE="${1:-_site}"
ids=$(grep -rhoE 'youtube(-nocookie)?\.com/embed/[A-Za-z0-9_-]{11}' "$SITE" --include='*.html' | sed -E 's#.*/##' | sort -u || true)
if [ -z "$ids" ]; then
  echo "Error: no YouTube embeds found in $SITE, the check would prove nothing" >&2
  exit 1
fi

fail=0
for id in $ids; do
  code=000
  for attempt in 1 2 3; do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
      "https://www.youtube.com/oembed?url=https://www.youtube.com/watch?v=${id}&format=json" || echo 000)
    [ "$code" = 200 ] || [ "$code" = 401 ] || [ "$code" = 403 ] || [ "$code" = 404 ] && break
    sleep 2
  done
  case "$code" in
    200) echo "ok: $id embeds" ;;
    401|403|404) echo "Error: YouTube video $id returned $code, it is private, removed or not embeddable" >&2; fail=1 ;;
    *) echo "warning: could not reach YouTube for $id (got $code), skipping" >&2 ;;
  esac
done
exit $fail
