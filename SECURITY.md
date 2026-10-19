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
opens only a literal loopback listener, with Host/Origin checks, strict query
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
