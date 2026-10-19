# Contributing

Use the latest patched stable Go release (currently verified: 1.27.2). Run
`make fmt`, `make check`, `make coverage` and `go mod tidy` before submitting.
Run `go -C tools mod tidy` when changing the isolated scanner module.
`make coverage` enforces at least 80% statement coverage per core package.
Tests must work
offline; use mock upstreams and deterministic times. Add regressions for changes
in behavior and avoid dependencies unless their rationale and official version
source are documented.

Keep domain data independent of VBB, HTTP and display integrations. Do not log
searches, full request URLs or upstream bodies. Do not add public listeners,
automatic publishing, releases or deployment. The workflows in
https://github.com/cpanato/pr-inbox were reviewed before introducing CI. Keep
actions/tools pinned after checking their official sources; see
[CI notes](docs/ci.md) for verification and updating instructions.
