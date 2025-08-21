#!/usr/bin/env bash

set -eou pipefail

CA_SRC=/app/ca.pem

if [ -f "$CA_SRC" ]; then
    echo "Found $CA_SRC, adding it to the trusted certificates..."

    OS_FAMILY=$(grep -i '^ID=' /etc/os-release | cut -d= -f2 | tr -d '"')
    case "$OS_FAMILY" in
        alpine | debian | ubuntu)
            DEST="/usr/local/share/ca-certificates/ca.crt"
            cp "$CA_SRC" "$DEST"
            update-ca-certificates
            ;;
        *)
            echo "Unsupported OS: $OS_FAMILY. You need to manually install CA cert."
            exit 1
            ;;
    esac

fi

exec gosu hocr /app/hocr
