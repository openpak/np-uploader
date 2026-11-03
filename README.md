# PS3 Uploader Server

OpenPak's PS3 attachment store: the firmware's `uploader` service that holds data too large for a message stanza.

| | |
| --- | --- |
| Network | PlayStation 3 |
| Role | `/uploader/sec/upload_data`, `download_data`, `delete_data` for message attachments (`inline="fileid"`) |
| Depends on | [`account`](https://github.com/openpak/account) (account core), [`np-common`](https://github.com/openpak/np-common) (vendored), [`title-storage`](https://github.com/openpak/title-storage) (gRPC) |
| Used by | [`np-account`](https://github.com/openpak/np-account) (forwards `/uploader/` via `NP_UPLOADER_URL`) |
| Image | `ghcr.io/openpak/np-uploader` |
| Licence | AGPL-3.0-only |

Attachments are not stored here: each is an object in the neutral `title-storage` core under the reserved
title key `np-messages` (collection `attachments`, named by the 32-hex reference, owner = the uploader).
The core holds the bytes and the per-account quota (1 MiB per attachment, 16 MiB and 200 per account,
declared at start); this service keeps the framing, the reference, the quota XML and the download rule
(the owner, or anyone the account core lets interact with the owner, by reference).

The wire format is static RE of firmware 4.93 and has not been confirmed against a console; see [docs/protocol.md](docs/protocol.md).

## Run

```sh
cp example.env .env   # fill in required values
set -a; . ./.env; set +a
GOWORK=off go run -mod=vendor .
```
