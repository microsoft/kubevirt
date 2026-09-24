#!/usr/bin/env bash
set -euo pipefail

merge_templates() {
    first=$1
    second=$2
    output=$3

    pk1=$(jq -cS '.properties.uefiSettings.signatures.PK' "$first")
    pk2=$(jq -cS '.properties.uefiSettings.signatures.PK' "$second")

    if [[ "$pk1" != "$pk2" ]]; then
        echo "error: templates have different PK values" >&2
        exit 1
    fi

    jq -s '
        def merge_database($templates; $name):
            [
                $templates[]
                | .properties.uefiSettings.signatures[$name][]?
                | .type as $type
                | .value[]
                | {type: $type, value: .}
            ]
            | unique_by([.type, .value])
            | group_by(.type)
            | map({
                type: .[0].type,
                value: map(.value)
            });

        . as $templates
        | $templates[0]
        | .properties.uefiSettings.signatures.KEK =
            merge_database($templates; "KEK")
        | .properties.uefiSettings.signatures.db =
            merge_database($templates; "db")
        | .properties.uefiSettings.signatures.dbx =
            merge_database($templates; "dbx")
    ' "$first" "$second" >"$output"

    echo "Created $output"
}

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <target-folder>" >&2
    exit 1
fi

target_dir=$1
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT

git clone --depth=1 https://github.com/microsoft/openvmm "$temp_dir/openvmm"

templates_dir="$temp_dir/openvmm/vm/devices/firmware/hyperv_secure_boot_templates/templates"

mkdir -p "$target_dir"

merge_templates \
    "$templates_dir/aarch64/MicrosoftUEFI_Template.json" \
    "$templates_dir/aarch64/MicrosoftWindows_Template.json" \
    "$target_dir/aarch64.json"

merge_templates \
    "$templates_dir/x64/MicrosoftUEFI_Template.json" \
    "$templates_dir/x64/MicrosoftWindows_Template.json" \
    "$target_dir/x64.json"

merge_templates \
    "$templates_dir/x64/MicrosoftUEFI_Confidential_Template.json" \
    "$templates_dir/x64/MicrosoftWindows_Confidential_Template.json" \
    "$target_dir/x64-confidential.json"
