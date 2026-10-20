# Security policy

Security fixes currently target the main branch during pre-release development.

Do not disclose vulnerabilities in public issues. Use GitHub's
[private vulnerability reporting](https://github.com/cpanato/berlin-transit/security/advisories/new)
if enabled; otherwise request a private reporting channel from the maintainer
without including vulnerability details publicly.

Include the affected revision, reproduction steps and expected impact. Relevant
issues include upstream destination restriction bypasses, unsafe parsing or
resource use, Host/Origin validation bypasses, stale-data misrepresentation,
sensitive data in errors/logs, and supply-chain compromise.

CLI queries make outbound VBB HTTPS requests. The explicit `serve` command
defaults to a literal loopback listener, with Host/Origin checks, strict query
validation, bounded cache/concurrency, rate limits and HTTP timeouts. Metrics
are disabled by default. There is no authentication; local access is trusted.
Do not expose the service through a public proxy or port forwarding. There is
no telemetry, deployment or automatic publishing. VBB needs no credentials. See
[API configuration](docs/api.md) for bounds and operational restrictions.

Optional TRMNL publishing is disabled by default and requires explicit operator
configuration. Treat `TRMNL_WEBHOOK_URL` as a credential: never commit it or include
it in issue reports, logs or fixtures. Only the official HTTPS private-plugin
host/path is supported; proxies and redirects are disabled and DNS answers are
validated before dialing. Scheduled VBB collection is a separate opt-in. Neither
capability opens a public listener. See [TRMNL configuration](integrations/trmnl/README.md)
for outbound data, limits, cache eligibility and virtual-device testing.

The optional [private Compose setup](docs/deployment.md) explicitly permits an
IPv4 wildcard only inside its isolated bridge network. Host publication stays
fixed to 127.0.0.1:8080; loopback defaults and HTTP Host/Origin checks remain in
place. The opt-in flag is not a LAN/public-access control and cannot make a host
wildcard safe on its own. Keep the example on a trusted local Docker host with
standard NAT/firewall rules and Engine 28+; no reverse proxy or remote access is
configured. The health probe is loopback-only and never fetches upstream data.
Local `.env` files are ignored by Git and excluded from the allowlisted Docker
build context. Keep file mode 600 and restrict access to Docker administration,
which can inspect container environment variables. Do not share resolved
Compose/environment inspection output containing the secret webhook URL.
