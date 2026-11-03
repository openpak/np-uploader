# Changelog: np-uploader

## 0.2.0

- Attachments move to the neutral `title-storage` core (gRPC) as objects under the reserved title key
  `np-messages` (collection `attachments`, owner = uploader). `PGStore`, the `uploader_data` table and
  the Postgres dependency are gone; the per-account quota is the title's policy in the core, declared at
  start. New env `NP_TITLE_STORAGE_ADDR` / `NP_TITLE_STORAGE_KEY`; drop `NP_UPLOADER_DATABASE_URL`. Framing,
  references, result codes and the download rule (owner, or anyone the account core lets interact with
  the owner) are unchanged.

## 0.1.2

- get_data_list and get_quota answer result 00 with the caller's own objects and usage. VSH 4.93 has no client code for either op, so the shapes are ours.

## 0.1.1

- Listens on :8535 by default (8533 is np-score).

## 0.1.0

- First release: upload_data, download_data and delete_data on Postgres. Wire format from static RE, unconfirmed on a console.
