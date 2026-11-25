package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// refGateBinary returns the meissa Go ref-gate executable used by the tests.
// The tests retain the fixture repository used by the former TypeScript suite.
func refGateBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("REF_GATE_BIN")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("ref-gate")
		if err != nil {
			t.Fatalf("find ref-gate: %v", err)
		}
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("ref-gate binary %s: %v", bin, err)
	}
	return bin
}

func runRefGate(t *testing.T, text string) (int, string) {
	t.Helper()
	bin := refGateBinary(t)
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "docs.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "docs.md")
	git("-c", "user.name=Ref Gate Tests", "-c", "user.email=ref-gate@example.invalid", "commit", "-qm", "fixture")
	config := filepath.Join(repo, "ref-gate.config.json")
	if err := os.WriteFile(config, mustReadConfig(t), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command := exec.CommandContext(t.Context(), bin, "--config", config)
	command.Dir = repo
	command.Env = append(os.Environ(), "GATE_ROOT="+repo)
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return 0, stderr.String()
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run ref-gate: %v", err)
	}
	return exitErr.ExitCode(), stderr.String()
}

func mustReadConfig(t *testing.T) []byte {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		path := filepath.Join(root, "ref-gate.config.json")
		if content, err := os.ReadFile(path); err == nil {
			return content
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("could not locate ref-gate.config.json")
		}
		root = parent
	}
}

func TestRefGateConfig(t *testing.T) {
	t.Run("allows only the exact gate name", func(t *testing.T) {
		for _, text := range []string{"orion-ref-gate", "`moon run orion-ref-gate:check` green", "(`tools/orion-ref-gate/index.test.ts`,"} {
			code, stderr := runRefGate(t, text)
			if code != 0 {
				t.Errorf("%q unexpectedly rejected with %d: %s", text, code, stderr)
			}
		}
	})
	t.Run("flags the private repo name and gate-name continuations", func(t *testing.T) {
		for _, text := range []string{"orion repo", "orion-ref-gate-private", "ORION-REF-GATE-private", "orion-ref-gateX", "orion-ref-gate_x", "orion-ref-gate and orion"} {
			code, stderr := runRefGate(t, text)
			if code == 0 {
				t.Errorf("%q unexpectedly passed", text)
			}
			if !strings.Contains(stderr, "docs.md") {
				t.Errorf("%q missing docs.md from stderr: %s", text, stderr)
			}
		}
	})
}
