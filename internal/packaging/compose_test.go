package packaging_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCompose(t *testing.T) {
	if os.Getenv("COMPOSE_CHECK") != "1" {
		t.Skip("run make compose-check to validate the Compose example")
	}
	t.Run("local", func(t *testing.T) { testCompose(t, false) })
	t.Run("release", func(t *testing.T) { testCompose(t, true) })
}

func testCompose(t *testing.T, release bool) {
	t.Helper()
	work := t.TempDir()
	for _, file := range []struct{ source, target string }{{"compose.yaml", "compose.yaml"}, {"compose.release.yaml", "compose.release.yaml"}, {".env.example", ".env"}} {
		data, err := os.ReadFile(filepath.Join("../..", file.source))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, file.target), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(work, ".env")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString("\nCOMPOSE_CHECK_LITERAL=${BERLIN_TRANSIT_COMPOSE_CANARY}\nBERLIN_TRANSIT_LISTEN_ADDR=192.0.2.2:9999\nBERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN=false\nBERLIN_TRANSIT_METRICS_ENABLED=true\n")
	closeErr := f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"compose", "--project-directory", work, "--env-file", path, "-f", filepath.Join(work, "compose.yaml")}
	if release {
		args = append(args, "-f", filepath.Join(work, "compose.release.yaml"))
	}
	args = append(args, "config", "--format", "json")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = work
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "COMPOSE_") && !strings.HasPrefix(entry, "BERLIN_TRANSIT_COMPOSE_CANARY=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "BERLIN_TRANSIT_COMPOSE_CANARY=must-not-expand")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Compose config: %v\n%s", err, out)
	}
	var model map[string]any
	if err := json.Unmarshal(out, &model); err != nil {
		t.Fatal(err)
	}
	services, ok := model["services"].(map[string]any)
	if !ok || len(services) != 1 {
		t.Fatal("expected one Compose service")
	}
	service := lookup(t, services, "berlin-transit")
	ports, ok := lookup(t, service, "ports").([]any)
	if !ok || len(ports) != 1 {
		t.Fatal("expected one published loopback port")
	}
	port := ports[0]
	for key, want := range map[string]string{"host_ip": `"127.0.0.1"`, "target": `8080`, "protocol": `"tcp"`} {
		equalJSON(t, lookup(t, port, key), want)
	}
	if number(t, lookup(t, port, "published")) != 8080 {
		t.Fatal("published port must be 8080")
	}
	for key, want := range map[string]string{
		"command": `["serve"]`, "user": `"65532:65532"`, "read_only": `true`, "cap_drop": `["ALL"]`,
		"cpus": `1`, "pids_limit": `64`, "restart": `"unless-stopped"`,
	} {
		equalJSON(t, lookup(t, service, key), want)
	}
	binary := "/berlin-transit"
	if release {
		binary = "/ko-app/berlin-transit"
		equalJSON(t, lookup(t, service, "image"), strconv.Quote(releaseImage))
		equalJSON(t, lookup(t, service, "pull_policy"), `"missing"`)
		if lookup(t, service, "build") != nil {
			t.Fatal("release override must remove the local build")
		}
	} else {
		equalJSON(t, lookup(t, service, "pull_policy"), `"never"`)
		equalJSON(t, lookup(t, service, "image"), `"berlin-transit:local"`)
	}
	opts, ok := lookup(t, service, "security_opt").([]any)
	if !ok {
		t.Fatal("missing security options")
	}
	noNewPrivileges := false
	for _, opt := range opts {
		noNewPrivileges = noNewPrivileges || opt == "no-new-privileges:true"
	}
	if !noNewPrivileges {
		t.Fatal("no-new-privileges is required")
	}
	for _, key := range []string{"privileged", "network_mode", "volumes", "devices"} {
		value := lookup(t, service, key)
		if value != nil && value != false && value != "" {
			t.Errorf("unexpected Compose %s: %v", key, value)
		}
	}
	for _, key := range []string{"mem_limit", "memswap_limit"} {
		if number(t, lookup(t, service, key)) != 128*1024*1024 {
			t.Errorf("unexpected %s", key)
		}
	}
	health := lookup(t, service, "healthcheck")
	equalJSON(t, lookup(t, health, "test"), `["CMD",`+strconv.Quote(binary)+`,"healthcheck"]`)
	equalJSON(t, lookup(t, health, "retries"), `3`)
	for key, want := range map[string]time.Duration{"interval": 30 * time.Second, "timeout": 3 * time.Second, "start_period": 10 * time.Second} {
		if duration(t, lookup(t, health, key)) != want {
			t.Errorf("unexpected healthcheck %s", key)
		}
	}
	if duration(t, lookup(t, service, "stop_grace_period")) != 15*time.Second {
		t.Error("unexpected stop grace period")
	}
	equalJSON(t, lookup(t, service, "logging", "options"), `{"max-file":"3","max-size":"5m"}`)
	env := lookup(t, service, "environment")
	literal := lookup(t, env, "COMPOSE_CHECK_LITERAL")
	if literal != "${BERLIN_TRANSIT_COMPOSE_CANARY}" && literal != "$${BERLIN_TRANSIT_COMPOSE_CANARY}" {
		t.Error("raw env value was interpolated")
	}
	for key, want := range map[string]string{
		"BERLIN_TRANSIT_LISTEN_ADDR": `"0.0.0.0:8080"`, "BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN": `"true"`,
		"BERLIN_TRANSIT_METRICS_ENABLED": `"false"`, "BERLIN_TRANSIT_COLLECTOR_ENABLED": `"false"`,
		"TRMNL_ENABLED": `"false"`, "TRMNL_WEBHOOK_URL": `""`,
		"BERLIN_TRANSIT_COLLECTOR_STATIONS": `"900100003,900100001"`,
	} {
		equalJSON(t, lookup(t, env, key), want)
	}
	network := lookup(t, model, "networks", "default")
	equalJSON(t, lookup(t, network, "driver"), `"bridge"`)
	for _, key := range []string{"enable_ipv6", "external"} {
		value := lookup(t, network, key)
		if value != nil && value != false {
			t.Errorf("unsafe network %s: %v", key, value)
		}
	}
	equalJSON(t, lookup(t, network, "driver_opts", "com.docker.network.bridge.host_binding_ipv4"), `"127.0.0.1"`)
	equalJSON(t, lookup(t, network, "driver_opts", "com.docker.network.bridge.gateway_mode_ipv4"), `"nat"`)
}

func number(t *testing.T, value any) float64 {
	t.Helper()
	switch v := value.(type) {
	case float64:
		return v
	case string:
		n, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return n
		}
	}
	t.Fatalf("invalid number: %v", value)
	return 0
}

func duration(t *testing.T, value any) time.Duration {
	t.Helper()
	if v, ok := value.(string); ok {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return time.Duration(number(t, value))
}
