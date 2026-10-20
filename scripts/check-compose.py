#!/usr/bin/env python3
"""Validate private Compose policy using examples only; never read operator .env."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix="berlin-transit-compose-") as work:
    work = Path(work)
    shutil.copyfile(root / "compose.yaml", work / "compose.yaml")
    shutil.copyfile(root / ".env.example", work / ".env")
    # Probe literal parsing and verify that fixed safety settings override .env.
    with (work / ".env").open("a") as fixture:
        fixture.write("\nCOMPOSE_CHECK_LITERAL=${BERLIN_TRANSIT_COMPOSE_CANARY}\n"
                      "BERLIN_TRANSIT_LISTEN_ADDR=192.0.2.2:9999\n"
                      "BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN=false\n"
                      "BERLIN_TRANSIT_METRICS_ENABLED=true\n")
    (work / ".env").chmod(0o600)
    env = {k: v for k, v in os.environ.items() if not k.startswith("COMPOSE_")}
    env["BERLIN_TRANSIT_COMPOSE_CANARY"] = "must-not-expand"
    command = ["docker", "compose", "--project-directory", str(work),
               "--env-file", str(work / ".env"), "-f", str(work / "compose.yaml"),
               "config", "--format", "json"]
    result = subprocess.run(command, cwd=work, env=env, capture_output=True, check=True)
    model = json.loads(result.stdout)

assert set(model["services"]) == {"berlin-transit"}
service = model["services"]["berlin-transit"]
ports = service["ports"]
assert len(ports) == 1
assert ports[0]["host_ip"] == "127.0.0.1"
assert ports[0]["target"] == 8080 and str(ports[0]["published"]) == "8080"
assert ports[0]["protocol"] == "tcp"
assert service["command"] == ["serve"]
assert service["user"] == "65532:65532" and service["read_only"]
assert service["cap_drop"] == ["ALL"]
assert "no-new-privileges:true" in service["security_opt"]
assert not service.get("privileged") and not service.get("network_mode")
assert not service.get("volumes") and not service.get("devices")
assert service["cpus"] == 1 and service["pids_limit"] == 64
assert int(service["mem_limit"]) == 128 * 1024 * 1024
assert int(service["memswap_limit"]) == int(service["mem_limit"])
assert service["restart"] == "unless-stopped"
assert service["pull_policy"] == "never"
assert service["healthcheck"]["test"] == ["CMD", "/berlin-transit", "healthcheck"]
assert service["healthcheck"]["retries"] == 3
assert str(service["healthcheck"]["interval"]) in {"30s", "30000000000"}
assert str(service["healthcheck"]["timeout"]) in {"3s", "3000000000"}
assert str(service["healthcheck"]["start_period"]) in {"10s", "10000000000"}
assert str(service["stop_grace_period"]) in {"15s", "15000000000"}
assert service["logging"]["options"] == {"max-file": "3", "max-size": "5m"}
settings = service["environment"]
# `config` may re-escape literal dollars for a round-trip-safe rendered file.
assert settings["COMPOSE_CHECK_LITERAL"] in {
    "${BERLIN_TRANSIT_COMPOSE_CANARY}", "$${BERLIN_TRANSIT_COMPOSE_CANARY}"
}
assert settings["BERLIN_TRANSIT_LISTEN_ADDR"] == "0.0.0.0:8080"
assert settings["BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN"] == "true"
assert settings["BERLIN_TRANSIT_METRICS_ENABLED"] == "false"
assert settings["BERLIN_TRANSIT_COLLECTOR_ENABLED"] == "false"
assert settings["TRMNL_ENABLED"] == "false" and not settings["TRMNL_WEBHOOK_URL"]
network = model["networks"]["default"]
assert network["driver"] == "bridge" and not network.get("enable_ipv6")
assert not network.get("external")
assert network["driver_opts"]["com.docker.network.bridge.host_binding_ipv4"] == "127.0.0.1"
assert network["driver_opts"]["com.docker.network.bridge.gateway_mode_ipv4"] == "nat"
print("Compose policy passed: loopback publication, restricted container, bounded resources, inert workers")
