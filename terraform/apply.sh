#!/usr/bin/env bash

set -eou pipefail

load_env() {
  export $(grep -Ev '^(#|GOOGLE_APPLICATION_CREDENTIALS|$)' "$1" | xargs) > /dev/null
}

if [ -f ../.env ]; then
  load_env ../.env
fi

if [ -f .env ]; then
  load_env .env
fi

# needed for backend state
mv main.tf main.tf.tmpl
envsubst < main.tf.tmpl > main.tf

terraform init -upgrade

terraform apply

mv main.tf.tmpl main.tf
