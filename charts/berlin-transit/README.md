# berlin-transit Helm chart

Optional private Kubernetes packaging; rendering/packaging never installs it.
Start with `make helm-check helm-package`. Installation requires explicit operator
authorization and an appropriate private cluster. No released image is assumed.
Build with `make image`, then make that image available on the intended nodes, or
select an operator-provisioned repository/digest and pull policy. Defaults use
`ko.local/berlin-transit:local` with `Never`. Only ko-built images are supported:
the executable probe path is `/ko-app/berlin-transit`.

The chart fixes one replica and `Recreate` strategy to avoid duplicate upstream
budgets and TRMNL sends during upgrades. Restarts clear cache, rate budget and
publisher deduplication. It creates a ClusterIP on port 8080 (optional via
`service.enabled=false`), never Ingress/NodePort/LoadBalancer, host networking or
host ports. Pods run as UID/GID 65532 with a read-only filesystem, dropped
capabilities, RuntimeDefault seccomp and no service account token. Resources and
exec startup/readiness/liveness probes are included. Probes call only local
readiness; a VBB outage does not restart the pod. Metrics are disabled.

## Network boundaries

NetworkPolicy is enabled by default. Ingress permits only same-namespace pods
matching `networkPolicy.clientLabels` (default `berlin-transit-client: "true"`)
on TCP 8080. Network policies require a supporting, enforcing CNI and are
additive: review other policies selecting these pods and your cluster's routing.
The Host header remains restricted to `localhost:8080` and `127.0.0.1:8080`.
Authorized cluster clients must send `Host: localhost:8080`; this is DNS-rebinding
protection, not authentication. Trusted cluster membership and network policy
provide access control. The chart does not make the unauthenticated API suitable
for the public internet.

After an authorized installation, local access can use
`kubectl port-forward --address 127.0.0.1 deployment/<release>-berlin-transit 8080:8080`.
Use the same port 8080 locally so normal browser/curl Host checks pass. Port
forwarding and node-to-pod traffic have CNI-specific policy behavior; verify it
in the intended cluster.

Default egress allows only UDP/TCP 53 to kube-system pods labeled
`k8s-app=kube-dns`. VBB requests are blocked until explicitly setting
`networkPolicy.allowPublicHTTPS=true`; the collector remains off by default.
Public HTTPS opt-in permits TCP 443 to IPv4 destinations excluding private,
loopback, link-local, CGNAT and reserved ranges. Standard NetworkPolicy cannot
express DNS names; the application separately enforces the VBB/TRMNL origins
and validates destination IPs. No IPv6 egress rule is supplied. If the cluster
uses NodeLocal DNS, different resolver labels, custom pod/service CIDRs or
nonstandard routing, review/adapt the policy before use. Public HTTPS must not
route to internally assigned public CIDRs. Disabling the policy removes this
boundary and requires equivalent external controls.

## Optional S-Bahn collection and TRMNL

An example values override selects S Buch (Berlin); station IDs remain configurable:

```yaml
networkPolicy:
  allowPublicHTTPS: true
collector:
  enabled: true
  stations: ["900135001"]
trmnl:
  enabled: true
  existingSecret: berlin-transit-trmnl
  webhookKey: trmnl-webhook-url
```

Provision the Secret separately using your existing secret-management process;
never put a real webhook in values, Git, command arguments, rendered fixtures or
CI artifacts. The chart only references a Secret key; it creates no Secret.
Collection requires 1–5 unique IDs; enabling TRMNL also requires collection and
an existing Secret name. Default intervals are 30s for collection and 5m for
TRMNL; default maximum departures is 8. Application startup validates duration
ranges and the secret URL. Helm schema rejects unknown values and unsafe service
types. No arbitrary environment injection is exposed by this chart.

Changes to external Secret data require a pod restart to reload environment
variables. No Secret value or checksum is rendered into Deployment annotations.
The chart has no persistent volume; all application state remains in memory.
See [local packaging policy](../../docs/releases.md) for build/acceptance limits.
