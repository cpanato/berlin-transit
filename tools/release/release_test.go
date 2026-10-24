package release_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"
)

type step struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}
type job struct {
	If          string            `yaml:"if"`
	Needs       string            `yaml:"needs"`
	Environment string            `yaml:"environment"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []step            `yaml:"steps"`
}
type workflow struct {
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]job    `yaml:"jobs"`
}

func load(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../..", path))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
func releaseWorkflow(t *testing.T) workflow {
	t.Helper()
	var w workflow
	load(t, ".github/workflows/release.yml", &w)
	return w
}

func TestReleaseBoundary(t *testing.T) {
	w := releaseWorkflow(t)
	if len(w.On) != 2 || w.On["workflow_dispatch"] == nil {
		t.Fatal("release must run only on version tags or explicit manual dispatch")
	}
	push, ok := w.On["push"].(map[string]any)
	if !ok || len(push) != 1 || !reflect.DeepEqual(push["tags"], []any{"v*"}) {
		t.Fatal("automatic releases must trigger only on v* tags")
	}
	if len(w.Permissions) != 0 {
		t.Fatal("workflow-wide permissions must remain empty")
	}
	validate := w.Jobs["validate"]
	condition := strings.Join(strings.Fields(validate.If), " ")
	want := "github.repository == 'cpanato/berlin-transit' && ((github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')) || (github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main' && inputs.publish))"
	if condition != want {
		t.Fatal("missing repository, version-tag or manual-confirmation guards")
	}
	if !reflect.DeepEqual(validate.Permissions, map[string]string{"contents": "read"}) {
		t.Fatal("validation must not have write permissions")
	}
	if len(validate.Steps) != 2 {
		t.Fatal("unexpected preflight steps")
	}
	check := validate.Steps[1].Run
	for _, guard := range []string{"refs/tags/$RELEASE_TAG^{commit}", "$GITHUB_SHA", `git merge-base --is-ancestor "$GITHUB_SHA" origin/main`} {
		if !strings.Contains(check, guard) {
			t.Errorf("missing authorization guard %q", guard)
		}
	}
	publish := w.Jobs["release"]
	if publish.Needs != "validate" || publish.Environment != "" {
		t.Fatal("publication must wait for validation without an environment approval gate")
	}
	if !reflect.DeepEqual(publish.Permissions, map[string]string{"contents": "write", "packages": "write", "id-token": "write", "attestations": "write"}) {
		t.Fatal("unexpected release permissions")
	}
	var image, artifacts bool
	for _, s := range publish.Steps {
		if strings.HasPrefix(s.Uses, "actions/checkout@") && s.With["ref"] != "${{ github.sha }}" {
			t.Fatal("checkout must match the source SHA recorded by provenance")
		}
		switch s.ID {
		case "provenance":
			image = strings.HasPrefix(s.Uses, "actions/attest@") && s.With["subject-name"] == "ghcr.io/cpanato/berlin-transit" && s.With["subject-digest"] == "${{ steps.image.outputs.digest }}" && s.With["push-to-registry"] == "true"
		case "artifacts":
			artifacts = strings.HasPrefix(s.Uses, "actions/attest@")
			for _, path := range []string{"dist/checksums.txt", "dist/*.tar.gz", "dist/*.zip", "dist/binary_*/berlin-transit*", "dist/sbom/*"} {
				artifacts = artifacts && strings.Contains(s.With["subject-path"], path)
			}
		}
	}
	if !image || !artifacts {
		t.Fatal("missing artifact or immutable image build provenance")
	}
	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/security.yml"} {
		var ordinary workflow
		load(t, path, &ordinary)
		if len(ordinary.Permissions) != 0 {
			t.Fatalf("unexpected global permissions in %s", path)
		}
		for _, j := range ordinary.Jobs {
			for _, permission := range []string{"contents", "packages", "id-token", "attestations"} {
				if j.Permissions[permission] == "write" {
					t.Errorf("%s must not grant %s write", path, permission)
				}
			}
			for _, s := range j.Steps {
				if strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@") && s.With["args"] != "check" {
					if !strings.Contains(s.With["args"], "--snapshot") || !strings.Contains(s.With["args"], "--skip=publish,sign") {
						t.Fatal("ordinary CI must skip publishing and signing")
					}
				}
			}
		}
	}
}

func TestReleaseTagsAndSignatures(t *testing.T) {
	var config struct {
		Kos []struct {
			Tags         []string `yaml:"tags"`
			Repositories []string `yaml:"repositories"`
			LocalDomain  string   `yaml:"local_domain"`
		} `yaml:"kos"`
		BinarySigns []struct {
			IDs  []string `yaml:"ids"`
			Args []string `yaml:"args"`
		} `yaml:"binary_signs"`
		Signs []struct {
			Artifacts string   `yaml:"artifacts"`
			Args      []string `yaml:"args"`
		} `yaml:"signs"`
		DockerSigns []struct {
			Artifacts string   `yaml:"artifacts"`
			Args      []string `yaml:"args"`
		} `yaml:"docker_signs"`
	}
	load(t, ".goreleaser.yaml", &config)
	if len(config.Kos) != 1 || config.Kos[0].LocalDomain != "ko.local" || !reflect.DeepEqual(config.Kos[0].Repositories, []string{"ghcr.io/cpanato/berlin-transit"}) {
		t.Fatal("unexpected local or published image destination")
	}
	for _, tc := range []struct {
		Name            string
		IsSnapshot      bool
		Tag, Prerelease string
		Want            []string
	}{
		{"snapshot", true, "v0.0.0", "", []string{"snapshot"}},
		{"stable", false, "v1.2.3", "", []string{"v1.2.3", "latest"}},
		{"prerelease", false, "v1.2.3-rc.1", "rc.1", []string{"v1.2.3-rc.1"}},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			var tags []string
			for _, source := range config.Kos[0].Tags {
				tmpl, err := template.New("tag").Parse(source)
				if err != nil {
					t.Fatal(err)
				}
				var result bytes.Buffer
				if err := tmpl.Execute(&result, tc); err != nil {
					t.Fatal(err)
				}
				if result.Len() > 0 {
					tags = append(tags, result.String())
				}
			}
			if !reflect.DeepEqual(tags, tc.Want) {
				t.Fatalf("tags %v, want %v", tags, tc.Want)
			}
		})
	}
	if len(config.BinarySigns) != 1 || !reflect.DeepEqual(config.BinarySigns[0].IDs, []string{"binary"}) {
		t.Fatal("binary signing must select application build")
	}
	if len(config.Signs) != 1 || config.Signs[0].Artifacts != "checksum" {
		t.Fatal("checksum list must be signed")
	}
	for _, args := range [][]string{config.BinarySigns[0].Args, config.Signs[0].Args} {
		if !reflect.DeepEqual(args, []string{"sign-blob", "--bundle=${signature}", "${artifact}", "--yes"}) {
			t.Fatal("blob signing must use verification bundles and keyless defaults")
		}
	}
	if len(config.DockerSigns) != 1 || config.DockerSigns[0].Artifacts != "manifests" || !reflect.DeepEqual(config.DockerSigns[0].Args, []string{"sign", "${artifact}@${digest}", "--yes"}) {
		t.Fatal("image signatures must bind the built manifest digest")
	}
}

func TestImageDigestSelection(t *testing.T) {
	w := releaseWorkflow(t)
	var script string
	for _, s := range w.Jobs["release"].Steps {
		if s.ID == "image" {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal("missing digest selection step")
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	item := func(repo, id, digest string) map[string]any {
		return map[string]any{"type": "Docker Manifest", "path": repo, "extra": map[string]string{"ID": id, "Digest": digest}}
	}
	for _, tc := range []struct {
		Name    string
		Entries []map[string]any
		OK      bool
	}{
		{"image", []map[string]any{item("ghcr.io/cpanato/berlin-transit:v1.2.3", "container", digest)}, true},
		{"same_digest_tags", []map[string]any{item("ghcr.io/cpanato/berlin-transit:v1.2.3", "container", digest), item("ghcr.io/cpanato/berlin-transit:latest", "container", digest)}, true},
		{"missing", nil, false},
		{"other_repository", []map[string]any{item("ghcr.io/cpanato/berlin-transit-other:v1.2.3", "container", digest)}, false},
		{"other_build", []map[string]any{item("ghcr.io/cpanato/berlin-transit:v1.2.3", "other", digest)}, false},
		{"invalid_digest", []map[string]any{item("ghcr.io/cpanato/berlin-transit:v1.2.3", "container", "latest")}, false},
		{"ambiguous", []map[string]any{item("ghcr.io/cpanato/berlin-transit:v1.2.3", "container", digest), item("ghcr.io/cpanato/berlin-transit:latest", "container", "sha256:"+strings.Repeat("b", 64))}, false},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			work := t.TempDir()
			if err := os.Mkdir(filepath.Join(work, "dist"), 0700); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(tc.Entries)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "dist/artifacts.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(work, "output")
			cmd := exec.Command("bash", "-e", "-u", "-o", "pipefail", "-c", script)
			cmd.Dir = work
			cmd.Env = append(os.Environ(), "IMAGE=ghcr.io/cpanato/berlin-transit", "GITHUB_OUTPUT="+output)
			result, err := cmd.CombinedOutput()
			if (err == nil) != tc.OK {
				t.Fatalf("selection result: %v\n%s", err, result)
			}
			if tc.OK {
				data, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != "digest="+digest+"\n" {
					t.Fatalf("unexpected digest output %q", data)
				}
			}
		})
	}
}
