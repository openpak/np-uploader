# Changelog: np-uploader

## 0.1.2

- get_data_list and get_quota answer result 00 with the caller's own objects and usage. VSH 4.93 has no client code for either op, so the shapes are ours.

## 0.1.1

- Listens on :8535 by default (8533 is np-score).

## 0.1.0

- First release: upload_data, download_data and delete_data on Postgres. Wire format from static RE, unconfirmed on a console.
