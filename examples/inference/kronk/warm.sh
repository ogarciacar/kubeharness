#!/bin/sh
set -eu
api=http://127.0.0.1:11435
want=$(ls /kronk/models/*/*/*.gguf | wc -l)
ids=$(curl -fsS "$api/v1/models" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
[ "$(printf '%s\n' $ids | grep -c .)" -eq "$want" ]
for id in $ids; do
  curl -fsS -o /dev/null -H 'Content-Type: application/json' \
    -d "{\"model\":\"$id\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":1}" \
    "$api/v1/chat/completions"
done
