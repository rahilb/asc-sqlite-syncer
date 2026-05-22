# asc-prometheus-exporter

A Prometheus exporter for App Store Connect. App Store Connect's own dashboards
are slow and awkward to alert on; this pulls the data over the ASC API on a
timer and serves it as Prometheus metrics you can graph and alert on like
anything else.

It exports three sources:

- **Sales / downloads** — units and developer proceeds from the daily Sales
  report.
- **Subscriptions** — active subscribers from the daily Subscription report.
- **Ratings & reviews** — total review count, average rating and a star
  histogram from the Customer Reviews API.

## How it works

Data is fetched on a background timer (default hourly) and cached. Prometheus
scrapes of `/metrics` always return the cached values, so scrapes are fast and
the ASC rate limits are never hit by scrape volume.

Sales and subscription data lag 1–2 days and some dates have no report, so the
exporter walks back from yesterday up to `REPORT_LOOKBACK_DAYS` and uses the
most recent report that has data. The report date in use is exported as
`asc_report_date_seconds`.

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
| `LISTEN_ADDR` | no | `:9844` | HTTP listen address. |
| `REFRESH_INTERVAL` | no | `1h` | How often to refresh from ASC. |
| `REPORT_LOOKBACK_DAYS` | no | `5` | How many days back to search for the latest report. |
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

./asc-prometheus-exporter
```

Then scrape `http://localhost:9844/metrics`. `/healthz` returns `ok` for
liveness checks.

Example Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: app-store-connect
    scrape_interval: 5m
    static_configs:
      - targets: ["localhost:9844"]
```

## Deploy with systemd

An example unit is in [`deploy/asc-prometheus-exporter.service`](deploy/asc-prometheus-exporter.service).
It runs as an unprivileged `DynamicUser` with the binary at
`/usr/local/bin/asc-prometheus-exporter` and config in an `EnvironmentFile`.

```sh
sudo install -m 0755 asc-prometheus-exporter /usr/local/bin/

# Config + secrets, readable only by the service.
sudo mkdir -p /etc/asc-prometheus-exporter
sudo cp .env.example /etc/asc-prometheus-exporter/asc.env
sudo chmod 0600 /etc/asc-prometheus-exporter/asc.env   # then edit it

# Put the .p8 key where StateDirectory will expose it and point
# ASC_PRIVATE_KEY_PATH at it, e.g.:
#   ASC_PRIVATE_KEY_PATH=/var/lib/asc-prometheus-exporter/AuthKey.p8

sudo cp deploy/asc-prometheus-exporter.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now asc-prometheus-exporter
journalctl -u asc-prometheus-exporter -f
```

## Metrics

| Metric | Labels | Description |
|---|---|---|
| `asc_sales_units` | `app, sku, country, product_type` | Units (downloads/sales) for the latest report date. |
| `asc_sales_proceeds` | `app, sku, currency` | Developer proceeds, summed per currency. |
| `asc_active_subscriptions` | `app, subscription, country` | Active subscribers (sum of all "Active …" columns). |
| `asc_app_reviews_total` | `app` | Total reviews reported by ASC paging metadata. |
| `asc_app_review_rating_avg` | `app` | Mean rating over the sampled reviews. |
| `asc_app_reviews_rating_count` | `app, rating` | Sampled review count per star (1–5). |
| `asc_report_date_seconds` | `report` | Unix timestamp of the report date in use. |
| `asc_refresh_success` | `source` | 1 if the last refresh of the source succeeded, else 0. |
| `asc_refresh_duration_seconds` | `source` | Duration of the last refresh. |
| `asc_refresh_timestamp_seconds` | `source` | Unix timestamp of the last refresh attempt. |

Standard Go runtime and process metrics are also exported.

A useful staleness alert:

```yaml
- alert: ASCExporterRefreshFailing
  expr: asc_refresh_success == 0
  for: 2h
  annotations:
    summary: "ASC exporter source {{ $labels.source }} failing to refresh"
```

## Caveats

- **`product_type`** values are Apple's raw Product Type Identifiers (e.g. `1F`
  free universal app, `IA1` in-app purchase, `IAY` auto-renewable subscription).
  See Apple's "App Store Connect product type identifiers" documentation.
- **Proceeds** are kept per currency because the report mixes currencies; do not
  sum `asc_sales_proceeds` across the `currency` label.
- **Review average** is computed over the sampled window (`REVIEWS_MAX`), newest
  first. `asc_app_reviews_total` is the true total from paging metadata. For
  apps with very many reviews, raise `REVIEWS_MAX` or treat the average as a
  recent-window figure.
- **Cardinality** — `country` on sales/subs and `product_type` can produce many
  series for apps selling in many territories. Disable a source you don't need,
  or pre-aggregate downstream.
- **Report versions** — Apple bumps these periodically. If a report 4xxs with an
  invalid-version error, the error message lists the valid versions; set
  `SALES_REPORT_VERSION` / `SUBSCRIPTION_REPORT_VERSION` accordingly.

## Development

```sh
go test ./...
go build -o asc-prometheus-exporter .
```
