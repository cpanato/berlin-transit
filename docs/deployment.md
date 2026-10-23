# Private local operations with Docker Compose

This is an optional, operator-managed deployment. Preparing/building/validating
these files starts no service. Only run the start commands below when you want
to run the application. CI never executes them. There is no registry publication,
release workflow, public listener, remote-access configuration or K3s deployment.

## Requirements and network boundary

Use a trusted local Docker host with **Engine 28.0.0 or newer**, a maintained
patched release, and **Compose 2.30.0 or newer** (raw env-file support). These
minimums are required for loopback isolation and raw environment parsing. Check
your installed tools with:

```sh
docker version
docker compose version
```

Docker's [port-publishing documentation](https://docs.docker.com/engine/network/port-publishing/)
explains loopback publication and the localhost/L2 isolation issue in older
Engine releases. The example explicitly maps **127.0.0.1:8080 → container:8080** on a
standard IPv4 NAT bridge. It enables no host networking, IPv6 publication,
external network, routed gateway, reverse proxy or public DNS. Keep Docker's
normal firewall/NAT controls enabled; do not enable custom direct routing or
change the host mapping to 0.0.0.0. Containers joined to the same bridge and
Docker administrators remain trusted. This is not authentication or a network
sandbox against a privileged host operator.

The process inside the isolated container binds 0.0.0.0:8080 only because
Compose explicitly sets `BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN=true`. Normal
CLI/service defaults stay loopback-only. The exception is for this container
boundary, not for running a public wildcard on the host. The HTTP API still
accepts only Host/Origin for 127.0.0.1:8080 or localhost:8080. Use those addresses;
container IPs/service-name URLs and other ports fail Host checks. A custom host
port requires deliberate application/Host-policy changes, not just port remapping.

## Local configuration

Create the local file once, without overwriting an existing configuration:

```sh
(umask 077; set -C; cat .env.example > .env)
```

Edit `.env` in your local editor. It is ignored by Git and excluded from the
allowlisted build context. Keep permissions at 600 and backups private. It is a
[raw Compose env_file](https://docs.docker.com/reference/compose-file/services/#env_file):
use literal `KEY=value`, with no quotes, interpolation or inline comments.
The example starts with collection and TRMNL both disabled, so startup and
healthchecks do not query VBB or send to TRMNL. S Buch (S2, ID 900135001) is only
an example station choice, and can be changed without editing source.

To opt in to scheduled VBB collection, set:

```dotenv
BERLIN_TRANSIT_COLLECTOR_ENABLED=true
BERLIN_TRANSIT_COLLECTOR_STATIONS=900135001
```

To additionally send cached normalized snapshots to your TRMNL plugin, set
`TRMNL_ENABLED=true` and enter its complete secret URL in `TRMNL_WEBHOOK_URL`
inside the file. This explicitly authorizes outbound transmission to TRMNL.
Never commit the URL or put it in command arguments, screenshots or support
reports. See the [adapter guide](../integrations/trmnl/README.md) for station,
interval, budget, quota and freshness semantics. Keep metrics off in this setup;
Compose fixes metrics to false and overrides listener settings from `.env`.

Environment-based credentials are visible to the Docker administrator through
container inspection. Restrict that access. `docker compose config` normally
prints resolved environment values, including secrets: use `--quiet`, and do
not share full config/inspect output. No credentials are baked into the image.

## Validate and build without starting

From the repository root:

```sh
# Safe CI-style policy validation: reads only .env.example in a temporary copy.
make compose-check
# Validate your real configuration without printing its values.
docker compose config --quiet
# Build the local-only image; does not start containers or publish it.
docker compose build
```

The policy check uses Go's standard library plus the installed Compose
parser. It creates no Docker resources and never reads your `.env`. Its assertions
cover publication, security restrictions, healthcheck, resource bounds, restart,
logging, network posture and disabled example workers.

The image uses the official Go builder pinned by immutable digest in [Dockerfile](../Dockerfile) and a
scratch runtime with bundled CA certificates/timezone data. It runs UID/GID
65532:65532, has no shell/curl and declares no EXPOSE. Compose adds a read-only
root filesystem, all capabilities dropped, no-new-privileges, 1 CPU, 128 MiB
memory with no extra swap allowance, 64 PIDs and bounded local logs (3 files,
5m each).
There are no host mounts, persistent application volumes or Docker socket.

## Explicitly start and verify

The commands in this section start a local deployment. Run them manually only
when you choose to operate it; they were not executed as part of preparation.
Port 8080 must be free: stop an earlier `test-trmnl.sh`/`serve` process first.

```sh
docker compose up -d --no-build --pull never
docker compose ps
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
docker compose exec -T berlin-transit /berlin-transit healthcheck
docker compose logs --tail=50 --follow berlin-transit
```

Image `berlin-transit:local` is built locally; `pull_policy: never` and the start
flags avoid registry pulls for this service image. The builder download is a
separate build-time dependency. No build/push/start occurs on its own.

The native `healthcheck` probes `/readyz` over literal loopback with a 2s timeout,
no proxy/redirect and a 1 KiB response limit. It does not construct a VBB client,
collect data or publish. Compose probes every 30s, allows a 10s startup period
and marks unhealthy after three failures, each with a 3s execution timeout.
Upstream VBB/TRMNL outages do not alone fail readiness. Check `trmnl_sent` or
sanitized delivery/collection failures in logs after enabling workers; device
render cadence is independent of publisher cadence.

`restart: unless-stopped` restarts an exited application and allows restart after
a Docker daemon/host restart once you have started it. A healthcheck becoming
unhealthy alone does **not** restart the container. Diagnose an unhealthy state
with the commands above and, if needed, explicitly recreate/restart it. Initial
Compose startup, health transitions and actual Docker host port isolation remain
manual operator smoke tests, rather than claims made by configuration validation.

## Update, stop and resume

Review changes before updating an operating instance. Fetch/fast-forward your
checkout to the reviewed main revision, then:

```sh
docker compose config --quiet
docker compose build
docker compose up -d --no-build --pull never --force-recreate
docker compose ps
```

Recreation reloads `.env` and the new image; `docker compose restart` alone does
not apply changed environment settings or images. The cache, last-success state,
rate budget and TRMNL deduplication are in memory and reset on recreation. Allow
a collection/publishing interval for data to return; avoid rapid restart loops
because upstream quotas are external to the process. Keep a known-good reviewed
revision/image for rollback and recreate explicitly if necessary.

```sh
# Stop this deployment; it stays stopped until explicitly started again.
docker compose stop
# Or stop and remove its containers and private network (keeps .env/local image).
docker compose down
# Resume an existing stopped container with its original environment/image.
docker compose start
```

If stopping TRMNL delivery while keeping the API, set `TRMNL_ENABLED=false` in
`.env` and recreate. Disable collection separately when no longer needed. Stop
commands do not erase a screen already rendered on TRMNL; judge it by its visible
collection timestamp. Deployment, LAN/VPN access, K3s, registries and releases
require separate operator decisions and are not configured here.

Official references:
[Compose services](https://docs.docker.com/reference/compose-file/services/),
[port publishing](https://docs.docker.com/engine/network/port-publishing/),
[Go releases](https://go.dev/dl/?mode=json),
[official builder tag](https://hub.docker.com/_/golang).

## Optional Kubernetes packaging

The [Helm chart](../charts/berlin-transit/README.md) provides private ClusterIP
manifests with restricted pods, local exec probes and NetworkPolicy. It supports
ko image entrypoints; the Dockerfile/Compose entrypoint differs. Render/package
with `make helm-check helm-package`; no installation is performed. Review the
chart guide's Host, DNS/CNI, secret and egress requirements before an explicitly
authorized deployment.
