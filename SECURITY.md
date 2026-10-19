# Security policy

Security fixes currently target the main branch during pre-release development.

Do not disclose vulnerabilities in public issues. Use GitHub's
[private vulnerability reporting](https://github.com/cpanato/berlin-transit/security/advisories/new)
if enabled; otherwise request a private reporting channel from the maintainer
without including vulnerability details publicly.

Include the affected revision, reproduction steps and expected impact. Relevant
issues include upstream destination restriction bypasses, unsafe parsing or
resource use, sensitive data in errors, and supply-chain compromise.

M1 is a local CLI with outbound VBB HTTPS requests and no listener, telemetry,
credentials, deployment or publishing. See README.md for transport restrictions.
