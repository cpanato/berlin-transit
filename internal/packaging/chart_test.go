package packaging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const releaseImage = "ghcr.io/cpanato/berlin-transit@sha256:0252caed0243176ca087138df2906f44a26e3c352f4397d6c4b86dbab2f6c793"

// These packaging tests use Helm's own YAML decoder and the standard library.
// They never contact a cluster. Ordinary Go tests do not require Helm installed.
func TestChart(t *testing.T) {
	helm := os.Getenv("HELM")
	if helm == "" {
		t.Skip("run make helm-check to exercise Helm policy fixtures")
	}
	helm, err := filepath.Abs(helm)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	chart := filepath.Join(work, "chart")
	if err := os.CopyFS(chart, os.DirFS("../../charts/berlin-transit")); err != nil {
		t.Fatal(err)
	}
	const probe = `{{- $objects := list -}}
{{- range $file := list "deployment.yaml" "service.yaml" "networkpolicy.yaml" -}}
{{- $rendered := include (print $.Template.BasePath "/" $file) $ | trim -}}
{{- if $rendered -}}
{{- $objects = append $objects ($rendered | fromYaml) -}}
{{- end -}}
{{- end -}}
{{ dict "objects" $objects | toJson }}
`
	if err := os.WriteFile(filepath.Join(chart, "templates/policy-test.yaml"), []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	render := func(t *testing.T, values string, valid bool) map[string]any {
		t.Helper()
		path := filepath.Join(work, "values.json")
		if err := os.WriteFile(path, []byte(values), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, helm, "template", "test", chart, "-f", path, "--show-only", "templates/policy-test.yaml").CombinedOutput()
		if !valid {
			if err == nil {
				t.Fatalf("invalid values accepted: %s", values)
			}
			if !bytes.Contains(out, []byte("values don't meet")) && !bytes.Contains(out, []byte("execution error")) {
				t.Fatalf("unexpected Helm failure: %s", out)
			}
			return nil
		}
		if err != nil {
			t.Fatalf("render: %v\n%s", err, out)
		}
		start := bytes.IndexByte(out, '{')
		if start < 0 {
			t.Fatalf("missing render JSON: %s", out)
		}
		var result struct {
			Objects []map[string]any `json:"objects"`
		}
		if err := json.Unmarshal(out[start:], &result); err != nil {
			t.Fatal(err)
		}
		objects := make(map[string]any)
		for _, object := range result.Objects {
			kind, ok := object["kind"].(string)
			if !ok {
				t.Fatalf("missing kind: %v", object)
			}
			objects[kind] = object
		}
		return objects
	}
	defaults := render(t, `{}`, true)
	if len(defaults) != 3 {
		t.Fatalf("unexpected resources: %v", defaults)
	}
	deployment := lookup(t, defaults, "Deployment", "spec")
	equalJSON(t, lookup(t, deployment, "replicas"), `1`)
	equalJSON(t, lookup(t, deployment, "strategy"), `{"type":"Recreate"}`)
	pod := lookup(t, deployment, "template", "spec")
	equalJSON(t, lookup(t, pod, "automountServiceAccountToken"), `false`)
	equalJSON(t, lookup(t, pod, "securityContext"), `{"runAsNonRoot":true,"runAsUser":65532,"runAsGroup":65532,"seccompProfile":{"type":"RuntimeDefault"}}`)
	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC", "volumes"} {
		if value := lookup(t, pod, field); value != nil {
			t.Errorf("unexpected %s: %v", field, value)
		}
	}
	c := lookup(t, pod, "containers", "0")
	equalJSON(t, lookup(t, c, "securityContext"), `{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"capabilities":{"drop":["ALL"]}}`)
	equalJSON(t, lookup(t, c, "image"), `"ko.local/berlin-transit:local"`)
	equalJSON(t, lookup(t, c, "imagePullPolicy"), `"Never"`)
	equalJSON(t, lookup(t, c, "args"), `["serve"]`)
	equalJSON(t, lookup(t, c, "ports"), `[{"name":"http","containerPort":8080,"protocol":"TCP"}]`)
	equalJSON(t, lookup(t, c, "resources", "limits"), `{"cpu":"1","memory":"128Mi"}`)
	for _, name := range []string{"startupProbe", "readinessProbe", "livenessProbe"} {
		equalJSON(t, lookup(t, c, name, "exec", "command"), `["/ko-app/berlin-transit","healthcheck"]`)
		equalJSON(t, lookup(t, c, name, "timeoutSeconds"), `3`)
	}
	env := environment(t, c)
	for _, name := range []string{"TRMNL_ENABLED", "BERLIN_TRANSIT_COLLECTOR_ENABLED", "BERLIN_TRANSIT_METRICS_ENABLED"} {
		equalJSON(t, lookup(t, env, name, "value"), `"false"`)
	}
	equalJSON(t, lookup(t, env, "BERLIN_TRANSIT_LISTEN_ADDR", "value"), `"0.0.0.0:8080"`)
	equalJSON(t, lookup(t, env, "BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN", "value"), `"true"`)
	if env["TRMNL_WEBHOOK_URL"] != nil {
		t.Fatal("default webhook must be absent")
	}
	svc := lookup(t, defaults, "Service", "spec")
	equalJSON(t, lookup(t, svc, "type"), `"ClusterIP"`)
	equalJSON(t, lookup(t, svc, "ports"), `[{"name":"http","port":8080,"targetPort":"http","protocol":"TCP"}]`)
	if !reflect.DeepEqual(lookup(t, svc, "selector"), lookup(t, deployment, "selector", "matchLabels")) {
		t.Fatal("service selector mismatch")
	}
	policy := lookup(t, defaults, "NetworkPolicy", "spec")
	equalJSON(t, lookup(t, policy, "policyTypes"), `["Ingress","Egress"]`)
	if !reflect.DeepEqual(lookup(t, svc, "selector"), lookup(t, policy, "podSelector", "matchLabels")) {
		t.Fatal("policy selector mismatch")
	}
	equalJSON(t, lookup(t, policy, "ingress"), `[{"from":[{"podSelector":{"matchLabels":{"berlin-transit-client":"true"}}}],"ports":[{"port":8080,"protocol":"TCP"}]}]`)
	equalJSON(t, lookup(t, policy, "egress"), `[{"to":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"kube-system"}},"podSelector":{"matchLabels":{"k8s-app":"kube-dns"}}}],"ports":[{"port":53,"protocol":"UDP"},{"port":53,"protocol":"TCP"}]}]`)
	t.Run("enabled", func(t *testing.T) {
		values := `{"collector":{"enabled":true,"stations":["900135001"]},"trmnl":{"enabled":true,"existingSecret":"test-existing-secret"},"networkPolicy":{"allowPublicHTTPS":true},"image":{"digest":"sha256:` + strings.Repeat("a", 64) + `"}}`
		objects := render(t, values, true)
		container := lookup(t, objects, "Deployment", "spec", "template", "spec", "containers", "0")
		equalJSON(t, lookup(t, container, "image"), `"ko.local/berlin-transit@sha256:`+strings.Repeat("a", 64)+`"`)
		enabledEnv := environment(t, container)
		equalJSON(t, enabledEnv["TRMNL_WEBHOOK_URL"], `{"name":"TRMNL_WEBHOOK_URL","valueFrom":{"secretKeyRef":{"name":"test-existing-secret","key":"trmnl-webhook-url"}}}`)
		equalJSON(t, lookup(t, enabledEnv, "BERLIN_TRANSIT_COLLECTOR_STATIONS", "value"), `"900135001"`)
		https := lookup(t, objects, "NetworkPolicy", "spec", "egress", "1")
		equalJSON(t, lookup(t, https, "ports"), `[{"port":443,"protocol":"TCP"}]`)
		equalJSON(t, lookup(t, https, "to", "0", "ipBlock", "cidr"), `"0.0.0.0/0"`)
		except, ok := lookup(t, https, "to", "0", "ipBlock", "except").([]any)
		if !ok {
			t.Fatal("missing egress exclusions")
		}
		for _, cidr := range []string{"10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16"} {
			found := false
			for _, item := range except {
				found = found || item == cidr
			}
			if !found {
				t.Errorf("missing excluded range %s", cidr)
			}
		}
	})
	for _, kind := range []string{"Service", "NetworkPolicy"} {
		key := "service"
		if kind == "NetworkPolicy" {
			key = "networkPolicy"
		}
		objects := render(t, `{"`+key+`":{"enabled":false}}`, true)
		if objects[kind] != nil {
			t.Errorf("%s still rendered when disabled", kind)
		}
	}
	for name, values := range map[string]string{
		"public_service":    `{"service":{"type":"LoadBalancer"}}`,
		"replicas":          `{"replicaCount":2}`,
		"empty_clients":     `{"networkPolicy":{"clientLabels":null}}`,
		"missing_stations":  `{"collector":{"enabled":true}}`,
		"missing_egress":    `{"collector":{"enabled":true,"stations":["900135001"]}}`,
		"duplicates":        `{"collector":{"stations":["same","same"]}}`,
		"too_many":          `{"collector":{"stations":["1","2","3","4","5","6"]}}`,
		"invalid_id":        `{"collector":{"stations":["bad/id"]}}`,
		"missing_collector": `{"trmnl":{"enabled":true,"existingSecret":"test"}}`,
		"missing_secret":    `{"collector":{"enabled":true,"stations":["900135001"]},"networkPolicy":{"allowPublicHTTPS":true},"trmnl":{"enabled":true}}`,
	} {
		t.Run(name, func(t *testing.T) { render(t, values, false) })
	}
	t.Run("release_examples", func(t *testing.T) {
		base, err := os.ReadFile("../../charts/berlin-transit/examples/release.yaml")
		if err != nil {
			t.Fatal(err)
		}
		trmnl, err := os.ReadFile("../../charts/berlin-transit/examples/trmnl.yaml")
		if err != nil {
			t.Fatal(err)
		}
		for _, enabled := range []bool{false, true} {
			t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
				values := string(base)
				if enabled {
					values += "\n" + string(trmnl)
				}
				objects := render(t, values, true)
				if len(objects) != 3 {
					t.Fatal("examples must only render Deployment, Service and NetworkPolicy")
				}
				container := lookup(t, objects, "Deployment", "spec", "template", "spec", "containers", "0")
				equalJSON(t, lookup(t, container, "image"), strconv.Quote(releaseImage))
				equalJSON(t, lookup(t, container, "imagePullPolicy"), `"IfNotPresent"`)
				// Image overrides must preserve all pod, service and ingress restrictions.
				for _, field := range []string{"securityContext", "resources", "ports", "startupProbe", "readinessProbe", "livenessProbe"} {
					if !reflect.DeepEqual(lookup(t, container, field), lookup(t, c, field)) {
						t.Errorf("release example changed container %s", field)
					}
				}
				if !reflect.DeepEqual(objects["Service"], defaults["Service"]) || !reflect.DeepEqual(lookup(t, objects, "NetworkPolicy", "spec", "ingress"), lookup(t, policy, "ingress")) {
					t.Error("release example changed private API access")
				}
				exampleEnv := environment(t, container)
				for _, name := range []string{"TRMNL_ENABLED", "BERLIN_TRANSIT_COLLECTOR_ENABLED"} {
					equalJSON(t, lookup(t, exampleEnv, name, "value"), strconv.Quote(strconv.FormatBool(enabled)))
				}
				if enabled {
					equalJSON(t, exampleEnv["TRMNL_WEBHOOK_URL"], `{"name":"TRMNL_WEBHOOK_URL","valueFrom":{"secretKeyRef":{"name":"berlin-transit-trmnl","key":"trmnl-webhook-url"}}}`)
					equalJSON(t, lookup(t, exampleEnv, "BERLIN_TRANSIT_COLLECTOR_STATIONS", "value"), `"900100003,900100001"`)
					equalJSON(t, lookup(t, objects, "NetworkPolicy", "spec", "egress", "1", "ports"), `[{"port":443,"protocol":"TCP"}]`)
				} else if exampleEnv["TRMNL_WEBHOOK_URL"] != nil || !reflect.DeepEqual(objects["NetworkPolicy"], defaults["NetworkPolicy"]) {
					t.Error("release-only example must keep webhook absent and HTTPS egress disabled")
				}
			})
		}
	})
}

func lookup(t *testing.T, value any, path ...string) any {
	t.Helper()
	for _, part := range path {
		switch node := value.(type) {
		case map[string]any:
			value = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				t.Fatalf("invalid index %q", part)
			}
			value = node[i]
		default:
			t.Fatalf("cannot look up %q in %T", part, value)
		}
	}
	return value
}

func equalJSON(t *testing.T, got any, want string) {
	t.Helper()
	var expected any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("got %v, want %s", got, want)
	}
}

func environment(t *testing.T, container any) map[string]any {
	t.Helper()
	entries, ok := lookup(t, container, "env").([]any)
	if !ok {
		t.Fatal("missing container env")
	}
	env := make(map[string]any)
	for _, item := range entries {
		name, ok := lookup(t, item, "name").(string)
		if !ok {
			t.Fatal("missing environment name")
		}
		if env[name] != nil {
			t.Fatalf("duplicate environment %s", name)
		}
		env[name] = item
	}
	return env
}
