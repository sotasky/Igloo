#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/../.."

scripts/dev/i18n_extract.sh

go test ./internal/i18n

for xml in android/app/src/main/res/values/strings.xml android/app/src/main/res/values-*/strings.xml; do
  [ -e "$xml" ] || continue
  xmllint --noout "$xml"
done

changed="$(git diff --name-only -- internal/components/*_templ.go locales/app/*.toml android/app/src/main/res/values*/strings.xml)"
if [ -n "$changed" ]; then
  echo "i18n generated files are out of date:" >&2
  echo "$changed" >&2
  git diff --stat -- internal/components/*_templ.go locales/app/*.toml android/app/src/main/res/values*/strings.xml >&2
  exit 1
fi

echo "[i18n] catalogs are current"
