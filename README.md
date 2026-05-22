# asc-prometheus-exporter

> Despite the name, this no longer exports to Prometheus. It syncs App Store
> Connect data into a **SQLite** database that Grafana reads directly. See
> [Why SQLite, not Prometheus](#why-sqlite-not-prometheus).

App Store Connect's own dashboards are slow and awkward to alert on. This pulls
the data over the ASC API on a timer and stores it in SQLite, so you can graph
and alert on it in Grafana.

It syncs three sources:

- **Sales / downloads** — units and developer proceeds from the daily Sales
  report.
- **Subscriptions** — active subscribers from the daily Subscription report.
- **Ratings & reviews** — total review count, average rating and a star
  histogram from the Customer Reviews API.

## Why SQLite, not Prometheus

ASC data is **low-volume daily aggregates that Apple restates** for a few days
after the fact. That is the opposite of what Prometheus assumes (high-frequency,
append-only, timestamped-at-scrape). Forcing it into Prometheus means:

- no clean backfill — scrapes are stamped at scrape time, not the report date;
- restatements can't overwrite history;
- the same daily value is re-scraped all day, so it looks like a counter but
  isn't, and `rate()`/`sum_over_time()` silently double-count.

A relational store fixes all of that. Each `(report_date, dimensions)` is a
unique row; re-syncing a date does a **delete-then-insert in a transaction**, so
restatements overwrite cleanly and series that vanished from a report are
removed. Backfill is just inserting past dates. SQLite needs zero infrastructure
— it's a single file Grafana can open.

## How it works

The daemon syncs on a timer (default every 6h). On the **first run** it
backfills `BACKFILL_DAYS` of history; every run after that re-pulls the last
`RESYNC_DAYS` so Apple's restatements are absorbed. A `backfilled` marker in the
DB makes the first-run backfill happen exactly once.

Reviews aren't date-keyed, so each sync stores a snapshot stamped with that day.

## Setup in App Store Connect

1. **API key** — App Store Connect → *Users and Access* → *Integrations* →
   *App Store Connect API*. Create a key (Admin or Finance role for reports),
   download the `.p8` file. Note the **Key ID** and the **Issuer ID**.
2. **Vendor number** — *Payments and Financial Reports* (top-left dropdown).
   Required for the sales and subscription reports.
3. **App Apple IDs** — the numeric ID from each app's *App Information* page (or
   the App Store URL). Required for the reviews source.

## Configuration

All config is via environment variables (see [`.env.example`](.env.example)).

| Variable | Required | Default | Description |
|---|---|---|---|
| `ASC_KEY_ID` | yes | — | API key ID (the JWT `kid`). |
| `ASC_ISSUER_ID` | yes | — | API issuer ID. |
| `ASC_PRIVATE_KEY_PATH` | yes\* | — | Path to the `.p8` private key. |
| `ASC_PRIVATE_KEY` | yes\* | — | Alternative: the `.p8` PEM contents directly (`\n`-escaped newlines accepted). |
| `ASC_VENDOR_NUMBER` | for sales/subs | — | Vendor number for reports. |
| `ASC_APP_IDS` | for reviews | — | Comma-separated app Apple IDs. |
| `ENABLE_SALES` | no | `true` | Toggle the sales source. |
| `ENABLE_SUBSCRIPTIONS` | no | `true` | Toggle the subscriptions source. |
| `ENABLE_REVIEWS` | no | `true` | Toggle the reviews source. |
| `SALES_REPORT_VERSION` | no | `1_1` | Sales report version. |
| `SUBSCRIPTION_REPORT_VERSION` | no | `1_4` | Subscription report version. |
| `DB_PATH` | no | `asc.db` | SQLite database file. |
| `BACKFILL_DAYS` | no | `90` | Days of history pulled on the first run. |
| `RESYNC_DAYS` | no | `5` | Recent days re-pulled each run (restatements). |
| `REFRESH_INTERVAL` | no | `6h` | Daemon sync interval (ignored with `--once`). |
| `REVIEWS_MAX` | no | `1000` | Max reviews paged through per app. |
| `HTTP_TIMEOUT` | no | `60s` | Per-request timeout against the ASC API. |

\* Provide either `ASC_PRIVATE_KEY_PATH` or `ASC_PRIVATE_KEY`.

## Run

```sh
go build -o asc-prometheus-exporter .

export ASC_KEY_ID=ABC123DEF4
export ASC_ISSUER_ID=00000000-0000-0000-0000-000000000000
export ASC_PRIVATE_KEY_PATH=/secrets/AuthKey_ABC123DEF4.p8
export ASC_VENDOR_NUMBER=80000000
export ASC_APP_IDS=1234567890
export DB_PATH=/var/lib/asc/asc.db

# Daemon (syncs on REFRESH_INTERVAL):
./asc-prometheus-exporter

# Or a single sync, e.g. from cron:
./asc-prometheus-exporter --once
```

Flags: `--once` (single sync then exit), `--debug` (verbose logging).

## Grafana

Install the [SQLite datasource plugin](https://grafana.com/grafana/plugins/frser-sqlite-datasource/)
and point it at `DB_PATH`. The DB uses WAL mode, so Grafana can read while the
daemon writes. Run Grafana on the same host (or share the file) since SQLite is
a local file.

Example panels (SQL):

```sql
-- Daily downloads (free app installs) over time
SELECT report_date AS time, SUM(units) AS downloads
FROM sales_units
WHERE product_type IN ('1','1F','1T','F1')
GROUP BY report_date ORDER BY report_date;

-- Daily proceeds in USD
SELECT report_date AS time, SUM(proceeds) AS usd
FROM sales_proceeds WHERE currency='USD'
GROUP BY report_date ORDER BY report_date;

-- Active subscribers by app
SELECT report_date AS time, app, SUM(active) AS subscribers
FROM active_subscriptions GROUP BY report_date, app ORDER BY report_date;

-- Latest average rating per app
SELECT app, rating_avg FROM reviews
WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM reviews);
```

## Schema

| Table | Key | Columns |
|---|---|---|
| `sales_units` | `(report_date, app, sku, country, product_type)` | `units` |
| `sales_proceeds` | `(report_date, app, sku, currency)` | `proceeds` |
| `active_subscriptions` | `(report_date, app, subscription, country)` | `active` |
| `reviews` | `(snapshot_date, app)` | `total`, `rating_avg` |
| `review_ratings` | `(snapshot_date, app, rating)` | `count` |
| `meta` | `key` | `value` (holds the `backfilled` marker) |

## Deploy with systemd

An example unit is in [`deploy/asc-prometheus-exporter.service`](deploy/asc-prometheus-exporter.service).
It runs as an unprivileged `DynamicUser`, with the binary at
`/usr/local/bin/asc-prometheus-exporter`, config in an `EnvironmentFile`, and a
writable `StateDirectory` for the `.p8` key and the SQLite database.

```sh
sudo install -m 0755 asc-prometheus-exporter /usr/local/bin/

sudo mkdir -p /etc/asc-prometheus-exporter
sudo cp .env.example /etc/asc-prometheus-exporter/asc.env
sudo chmod 0600 /etc/asc-prometheus-exporter/asc.env   # then edit it
# Point DB_PATH and ASC_PRIVATE_KEY_PATH inside the state dir, e.g.:
#   DB_PATH=/var/lib/asc-prometheus-exporter/asc.db
#   ASC_PRIVATE_KEY_PATH=/var/lib/asc-prometheus-exporter/AuthKey.p8

sudo cp deploy/asc-prometheus-exporter.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now asc-prometheus-exporter
journalctl -u asc-prometheus-exporter -f
```

For a cron-style single sync instead of the daemon, run `--once` from a
systemd timer or crontab.

## Caveats

- **`product_type`** values are Apple's raw Product Type Identifiers (e.g. `1F`
  free universal app, `IA1` in-app purchase, `IAY` auto-renewable subscription).
  See Apple's "App Store Connect product type identifiers" documentation.
- **Proceeds** are stored per currency because the report mixes currencies; sum
  within a `currency`, not across.
- **Review average** is over the sampled window (`REVIEWS_MAX`), newest first.
  `total` is the true count from paging metadata.
- **Backfill depth** is bounded by how far back ASC still serves daily reports
  (months, not unlimited). Days with no report are skipped.
- **Report versions** — Apple bumps these. If a report 4xxs with an
  invalid-version error, the message lists valid versions; set
  `SALES_REPORT_VERSION` / `SUBSCRIPTION_REPORT_VERSION` accordingly.

## Development

```sh
go test ./...
go build -o asc-prometheus-exporter .
```
