# Optional TRMNL private plugin (M5)

The collector and private JSON API work without TRMNL. Both periodic collection
and TRMNL output are **disabled by default**, and run only with `serve` after
operator opt-in. No inbound TRMNL route, public listener, image publication or
deployment is needed. This integration has no dependency on `tado-bridge`.

## Test the layout on your virtual device

A physical display is unnecessary. TRMNL's [virtual device](https://help.trmnl.com/en/articles/12410512-what-is-a-virtual-device)
lets eligible developer accounts create private plugins before hardware arrives.
Use your existing virtual device and its private-plugin editor for this test.
The [Private Plugins guide](https://help.trmnl.com/en/articles/9510536-private-plugins)
explains developer/BYOD access. A virtual device is distinct from
[Mirror](https://help.trmnl.com/en/articles/11647459-trmnl-for-ipad), which does not
support virtual devices.

1. Build a flat ZIP containing only `settings.yml`, `full.liquid`,
   `half_horizontal.liquid`, `half_vertical.liquid` and `quadrant.liquid`:

   ```sh
   make trmnl-package
   ```

2. In your virtual device's account, go to Plugins → Private Plugin → Import new
   and import `dist/berlin-transit-trmnl.zip`. The strategy is **Webhook**.
   Keep it private; publishing a Recipe is unnecessary.
3. Open Edit Markup. Use the editor's data/variables preview with the object in
   `sample-variables.json` (not wrapped in `merge_variables`) and preview all four
   layouts. These are synthetic, fixed-time fixtures, not live departures.
   If the editor's data controls differ, use its sample-data editor or wait for
   the real webhook step below to populate Your Variables.
4. Check destination escaping, planned vs predicted time, zero vs unknown delay,
   cancellation, empty departures, `CACHED`, collection date/time/offset and the
   title bar. Try the stale station alone in a small layout. Dates in the fixture
   are deliberately fixed; adjust them for your manual preview if desired.

The templates use the official [framework](https://trmnl.com/framework) and
[Liquid screen conventions](https://docs.trmnl.com/go/private-plugins/templates),
with no scripts, external assets or full-page/view wrappers. All provider text
is escaped. Full screen displays up to two stations × three departures;
half horizontal two × one; half vertical one × three; quadrant one × one.
Additional stations are explicitly counted. Adjust the limits after checking
fit on your device profile. The API never applies these display limits.

## Test the real integration

Save the plugin, then copy its **Webhook URL** from settings into a local secret
environment variable. It contains a Plugin Setting UUID/token; it is not a
Device API Key or User API Key. Keep it out of chat, Git, shell history and logs.
The client accepts only `https://trmnl.com/api/custom_plugins/<token>`, with no
query, userinfo, custom port, fragment or redirects. Legacy hostnames and BYOS
servers are not supported by this first adapter.

Build with `make build`, then opt in from a local terminal. This example uses
Bash's silent prompt, so the URL is not part of the command history:

```sh
bash
read -r -s -p 'TRMNL webhook URL: ' TRMNL_WEBHOOK_URL
printf '\n'
export TRMNL_WEBHOOK_URL
export BERLIN_TRANSIT_COLLECTOR_ENABLED=true
export BERLIN_TRANSIT_COLLECTOR_STATIONS=900100003
export BERLIN_TRANSIT_COLLECTOR_INTERVAL=30s
export BERLIN_TRANSIT_COLLECTOR_LIMIT=8
export TRMNL_ENABLED=true
export TRMNL_INTERVAL=5m
export TRMNL_MAX_DEPARTURES=8
./dist/berlin-transit serve
# Ctrl+C stops HTTP and both workers. Then:
unset TRMNL_WEBHOOK_URL TRMNL_ENABLED BERLIN_TRANSIT_COLLECTOR_ENABLED
exit
```

Enabling collection authorizes the configured VBB queries. Enabling TRMNL
explicitly sends only the selected stations' normalized public departure data
and freshness metadata to TRMNL's hosted service. There are no other outbound
notifications or telemetry. The listener remains loopback-only.

The collector starts with the first configured station, then rotates at
`COLLECTOR_INTERVAL / station_count` plus small jitter, waiting after each fetch.
The publisher first checks the cache on startup, then every five minutes plus
jitter. The first check can precede collection, so allow one full publishing
interval for the first payload. Watch for `trmnl_sent`, then check the plugin's
Your Variables and preview. Force a render/refresh in the UI when available.
TRMNL may defer rendering to the plugin's refresh schedule (15 minutes in the
imported settings); device/playlist refresh is a separate cadence. A successful
webhook is not proof that the device has refreshed.

You can independently inspect the warmed cache through the API with the same
options, e.g. `/api/v1/stations/900100003/departures?limit=8`. Different limits,
modes, duration or direction intentionally use different cache keys. The API
can use the global budget and cause a collection tick to fail; collection never
gets a separate burst or priority bypass.

Stop/restart with `TRMNL_ENABLED=false` to verify that the API and optional
collector work alone. Offline tests simulate webhook errors without sending to
a real device. Real account access, import and visual checks are operator steps;
no account credentials or real webhook are in the repository.

## Configuration

| Variable | Default | Bounds / meaning |
| --- | --- | --- |
| `BERLIN_TRANSIT_COLLECTOR_ENABLED` | `false` | Explicit scheduled VBB collection opt-in |
| `BERLIN_TRANSIT_COLLECTOR_STATIONS` | empty | 1–5 distinct comma-separated opaque IDs when enabled |
| `BERLIN_TRANSIT_COLLECTOR_INTERVAL` | `30s` | 30s–1h per full rotation; at least station count × upstream budget spacing |
| `BERLIN_TRANSIT_COLLECTOR_MODE` | `suburban` | Same modes as the API |
| `BERLIN_TRANSIT_COLLECTOR_LIMIT` | `8` | 1–50 per station |
| `BERLIN_TRANSIT_COLLECTOR_DURATION` | `30` | 1–120 minutes |
| `BERLIN_TRANSIT_COLLECTOR_DIRECTION` | empty | Optional provider stop ID |
| `TRMNL_ENABLED` | `false` | Requires collection enabled; explicit outbound publication opt-in |
| `TRMNL_WEBHOOK_URL` | empty | Secret official private-plugin webhook, required when enabled |
| `TRMNL_INTERVAL` | `5m` | 5m–24h minimum between sends, including retries |
| `TRMNL_MAX_DEPARTURES` | `8` | 1–8 per selected station, bounded by what was collected |

These environment settings have no CLI flag equivalents. Invalid settings fail
at startup without echoing their values. The existing API/cache configuration
still applies. For five stations and a one-request/minute VBB budget, choose a
collection interval of at least five minutes. Retries, HTTP traffic and upstream
cooldowns can further slow the rotation. Collection reuses fresh cache entries;
it does not force a refresh or override upstream cache restrictions.

## Payload, bounds and failure behavior

The [official webhook contract](https://docs.trmnl.com/go/private-plugins/webhooks)
uses a POST with `{"merge_variables": {...}}`. This adapter sends replacement
snapshots, never accumulating historical data. The sample file shows the inner
object: generation time, grouped stations with collected time and freshness,
departures, source, and IDs for unavailable configured stations. Times are
RFC3339; collection times retain Europe/Berlin offsets. Unknown departure fields
remain null, including unknown delay. Stale snapshots never claim realtime.

The publisher's source interface exposes **cache reads only**. It never requests
VBB data and never retains a second historical cache. No prior success, eviction,
`no-store`, `no-cache`, or expired data outside `MAX_STALE` means unavailable.
`must-revalidate` snapshots are unavailable after expiry. Expired eligible data
is explicitly stale, including last-success time and age. If all selected
stations are unavailable, publishing is skipped; otherwise missing IDs are
reported alongside available groups. Each send rechecks the latest cache.

The whole serialized request is capped at **5000 bytes**, conservatively within
the standard 5 KB merge-variable allowance. Oversized payloads are skipped,
without truncating fields or sending malformed data. Reduce stations or
`TRMNL_MAX_DEPARTURES` for a large payload. TRMNL+ increased quotas are deliberately
unused. Standard accounts allow 12 sends/hour: the minimum five-minute interval
also applies to retries. Only network failures, 429 and 500/502/503/504 receive
up to two retries for unchanged content, on later ticks with exponential
backoff and jitter. Retry-After seconds/HTTP dates impose a shared publisher
cooldown, capped at 24 hours. Other errors do not retry the same snapshot.

There is one in-flight delivery and no backlog/spool. A changed snapshot replaces
the pending content, retaining any cooldown. Successfully sent identical
snapshots are deduplicated (generation time alone does not trigger a send).
All state is in memory; restart resets deduplication and quotas, so avoid rapid
restarts. The client has a 10s timeout, one connection, 16 KiB response headers,
4 KiB bounded response discard, disabled proxies/redirects and a fixed official
host with validated public DNS addresses dialed directly under normal TLS
verification. Logs contain only fixed event/error categories, never the URL,
station list, request or upstream body.

When data or connectivity disappears, an already-rendered screen can remain on
the device. Always judge it by the visible collection timestamp. There is no
promise of realtime accuracy, automated screen expiry or five-minute display
refresh. Physical e-ink behavior is outside the offline tests; complete the
virtual-device preview and then check on hardware if available.

Official contracts and Go's patched stable feed were checked on 2026-10-19.
No new runtime or development dependencies are required.
