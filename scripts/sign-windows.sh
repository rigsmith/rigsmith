#!/bin/sh
# Authenticode-sign a Windows .exe with Azure Trusted Signing via jsign, on a
# non-Windows runner. GoReleaser calls this from each build's post hook for every
# target; it no-ops unless the target is Windows AND the Trusted Signing secrets
# are present — so unsigned releases still work before the secrets are added.
#
#   $1  path to the built binary
#   $2  GOOS of the build
#
# Env (from the release workflow):
#   AZURE_TENANT_ID / AZURE_CLIENT_ID / AZURE_CLIENT_SECRET  service principal
#   TRUSTED_SIGNING_ENDPOINT   e.g. https://eus.codesigning.azure.net
#   TRUSTED_SIGNING_ACCOUNT    Trusted Signing account name
#   TRUSTED_SIGNING_PROFILE    certificate profile name
#   JSIGN_JAR                  path to the jsign jar
set -eu

bin="$1"
os="$2"

[ "$os" = "windows" ] || exit 0

if [ -z "${TRUSTED_SIGNING_ACCOUNT:-}" ]; then
  echo "windows signing: TRUSTED_SIGNING_ACCOUNT unset — skipping (unsigned: $bin)" >&2
  exit 0
fi

: "${AZURE_TENANT_ID:?}" "${AZURE_CLIENT_ID:?}" "${AZURE_CLIENT_SECRET:?}"
: "${TRUSTED_SIGNING_ENDPOINT:?}" "${TRUSTED_SIGNING_PROFILE:?}" "${JSIGN_JAR:?}"

# Client-credentials access token for the code-signing audience.
token=$(curl -fsS -X POST "https://login.microsoftonline.com/${AZURE_TENANT_ID}/oauth2/v2.0/token" \
  --data-urlencode "client_id=${AZURE_CLIENT_ID}" \
  --data-urlencode "client_secret=${AZURE_CLIENT_SECRET}" \
  --data-urlencode "grant_type=client_credentials" \
  --data-urlencode "scope=https://codesigning.azure.net/.default" |
  jq -r .access_token)

[ -n "$token" ] && [ "$token" != "null" ] || {
  echo "windows signing: could not obtain an Azure access token" >&2
  exit 1
}

echo "windows signing: $bin -> Trusted Signing (${TRUSTED_SIGNING_ACCOUNT}/${TRUSTED_SIGNING_PROFILE})" >&2
java -jar "$JSIGN_JAR" \
  --storetype TRUSTEDSIGNING \
  --keystore "$TRUSTED_SIGNING_ENDPOINT" \
  --storepass "$token" \
  --alias "${TRUSTED_SIGNING_ACCOUNT}/${TRUSTED_SIGNING_PROFILE}" \
  --tsaurl "http://timestamp.acs.microsoft.com" \
  "$bin"
