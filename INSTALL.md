# NetBox 4.7 custom fields fix

This fork fixes HTTP 400 errors such as `Invalid value for custom field 'fond':
Invalid choice ({'label': 'Будущее', 'value': 'future'})` during VM synchronization.

PATCH now includes only changed custom field keys. NetBox merges them with the
stored values; untouched user fields are not replayed in API read format. This
merge behavior was checked in the NetBox v4.2.0 and v4.7.0 serializers:

- https://github.com/netbox-community/netbox/blob/v4.2.0/netbox/extras/api/customfields.py
- https://github.com/netbox-community/netbox/blob/v4.7.0/netbox/extras/api/customfields.py

For changed fields, the inventory snapshots the custom field definitions already
loaded from NetBox. Selection values use their `value`, object references use
IDs, and multiselection/multiobject values use lists of those values. Comparison
uses the same representations so another run does not issue redundant PATCHes.
JSON and unknown field types remain unmodified, including dictionaries containing
`value`, `label`, or `id`. Omitted fields are preserved; explicit null clears are
still sent. Existing source priority and tag merging rules remain in place.

VM identity, cluster matching, MAC/IP synchronization, reconciler code, and
preflight/systemd configuration are unchanged. In particular, an unknown VM still
requires the existing reconciler registration procedure.

## Validation

Regression tests cover untouched selection/multiselection/object/multiobject and
JSON fields, typed changes and comparisons, explicit clears, source priorities,
`Fond`/`fond` VMware name normalization, and two AddVM passes through a local HTTP
API. The mock rejects replayed user fields and preserves VM ID 3219.

The cloud environment cannot resolve public DNS: the full race-enabled suite
reports failures in `TestLookup` and `TestReverseLookup`. These are existing
network-dependent checks, not skipped or changed by this fix. Live NetBox/vCenter
validation was not available. The invalid-host HTTP test needs only its deliberately
invalid hostname appended to existing `NO_PROXY` and `no_proxy` lists when an
HTTP proxy is in use.

## Build

Use Go 1.26.9 (or a compatible newer toolchain) and the existing checkout:

```sh
export GOFLAGS=-mod=readonly
export NO_PROXY="${NO_PROXY:+$NO_PROXY,}invalid-host-that-does-not-exist"
export no_proxy="${no_proxy:+$no_proxy,}invalid-host-that-does-not-exist"
go mod download
go mod verify
go test -race ./...
# Linter version used by repository CI:
golangci-lint run --timeout=5m

build_commit=$(git rev-parse HEAD)
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=v1.26.1-customfields.1 -X main.commit=$build_commit -X main.date=$build_date" \
  -o netbox-ssot ./cmd/netbox-ssot
./netbox-ssot -h
```

Use `GOARCH=arm64` for Linux ARM64. If the Go module proxy's storage redirect is
blocked, `GOPROXY=direct go mod download` can use GitHub instead; keep checksum
and TLS verification enabled. Do not run `go mod tidy` as an installation step.

## Download the prebuilt application

Linux amd64 and arm64 archives, `SHA256SUMS`, and exact source/build details are
published on the separate
[builds/custom-fields-fix branch](https://github.com/Nectenebis/netbox-ssot-custom-tag-fix/tree/builds/custom-fields-fix).
Compiled binaries are kept out of the source branch and source PRs.

Download the archive for `uname -m` (`x86_64` means amd64; `aarch64` means arm64)
and `SHA256SUMS` into an empty staging directory. Verify the selected archive:

```sh
sha256sum --ignore-missing -c SHA256SUMS
# Must report OK for the archive you downloaded. Then extract it:
tar -xzf netbox-ssot_v1.26.1-customfields.1_linux_amd64.tar.gz
./netbox-ssot -h
```

## Replace the server binary and retain rollback

No server files are changed by publishing this fix. The following steps are for
an administrator on the server, after checking the staging download.

First check the actual service binary path with
`systemctl show -p ExecStart --value netbox-ssot.service`. The example below assumes
`/opt/netbox-ssot/netbox-ssot`; change it if ExecStart uses another binary. Keep
all configuration, credentials, SQLite mappings, and ExecStartPre commands intact.
Do not bypass a failing reconciler preflight.

Run in the staging directory, keeping the shell open for the rollback variables:

```sh
binary=$(readlink -f /opt/netbox-ssot/netbox-ssot)
backup="${binary}.before-customfields-$(date -u +%Y%m%dT%H%M%SZ)"
timer_was_active=$(systemctl is-active netbox-ssot.timer || true)
sudo systemctl stop netbox-ssot.timer
sudo systemctl stop netbox-ssot.service
sudo cp -a -- "$binary" "$backup"
sudo install -m 0755 ./netbox-ssot "${binary}.new"
sudo mv -f -- "${binary}.new" "$binary"
"$binary" -h
# This runs the existing preflight and normal sync with the existing config:
sudo systemctl start netbox-ssot.service
sudo journalctl -u netbox-ssot.service -n 80 --no-pager
# Restore the previous timer state only after checking the result:
if [ "$timer_was_active" = active ]; then sudo systemctl start netbox-ssot.timer; fi
```

Check that the startup banner shows `v1.26.1-customfields.1`, the sync completes,
and `fond`, `environment`, and the VM's NetBox ID remain correct. A source-provided
choice must contain the technical value (for example `future`), not its display
label; invalid source values remain validation errors.

To roll back to the saved binary, using the same `binary`, `backup`, and
`timer_was_active` values:

```sh
sudo systemctl stop netbox-ssot.timer
sudo systemctl stop netbox-ssot.service
sudo cp -a -- "$backup" "${binary}.rollback"
sudo mv -f -- "${binary}.rollback" "$binary"
"$binary" -h
if [ "$timer_was_active" = active ]; then sudo systemctl start netbox-ssot.timer; fi
```

Rollback changes the executable only; it does not undo NetBox updates from any
completed synchronization.
