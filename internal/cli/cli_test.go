package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuna-ai/luuna-beacon/internal/model"
)

func runAt(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func requireOK(t *testing.T, code int, out, errOut string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

func TestCorePromptWorkflow(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAt(t, dir, "--json", "init")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "create", "--name", "review", "--description", "Review code", "--section", "Task: inspect changes")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "update", "review", "--status", "archived")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "list")
	requireOK(t, code, out, errOut)
	if strings.Contains(out, "review") {
		t.Fatalf("archived prompt appeared in default list: %s", out)
	}
	code, out, errOut = runAt(t, dir, "--json", "list", "--status", "archived")
	requireOK(t, code, out, errOut)
	if !strings.Contains(out, "review") {
		t.Fatalf("archived prompt missing from explicit list: %s", out)
	}
}

func TestLegacyTagRoutingAndSchemaRule(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAt(t, dir, "init")
	requireOK(t, code, out, errOut)
	config := "project: test\nversion: \"1\"\nemitDirByTag:\n  command: .claude/commands\n"
	if err := os.WriteFile(filepath.Join(dir, ".luuna", "beacon", "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runAt(t, dir, "create", "--name", "hello", "--tag", "command", "--section", "Body: hi")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "emit", "hello")
	requireOK(t, code, out, errOut)
	if _, err := os.Stat(filepath.Join(dir, ".claude", "commands", "hello.md")); err != nil {
		t.Fatalf("legacy tag route was not used: %v", err)
	}
	code, out, errOut = runAt(t, dir, "schema", "create", "--name", "safe", "--required", "Task")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "schema", "rule", "add", "safe", "--section", "Task", "--pattern", "review", "--message", "must review")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "schema", "show", "safe")
	requireOK(t, code, out, errOut)
	var response map[string]any
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "must review") {
		t.Fatalf("rule was not persisted: %s", out)
	}
}

func TestImportAndUpdateFrontmatterAndInheritanceRemoval(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAt(t, dir, "init")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "create", "--name", "base", "--section", "Role=parent")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "create", "--name", "child", "--extends", "base")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "update", "child", "--remove-section", "Role", "--fm", "owner=team")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "render", "child")
	requireOK(t, code, out, errOut)
	if strings.Contains(out, "parent") || !strings.Contains(out, "owner") {
		t.Fatalf("unexpected resolved child: %s", out)
	}
	input := filepath.Join(dir, "existing.md")
	if err := os.WriteFile(input, []byte("---\ndescription: Imported prompt\ncategory: examples\n---\n## Task\n\nReview it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runAt(t, dir, "--json", "import", input, "--name", "imported", "--tag", "example")
	requireOK(t, code, out, errOut)
	if !strings.Contains(out, "\"command\": \"import\"") {
		t.Fatalf("wrong import response: %s", out)
	}
}

func TestUnversionedLookupUsesCurrentNameNotHistoricalRecord(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAt(t, dir, "init")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "create", "--name", "a", "--section", "Body=old")
	requireOK(t, code, out, errOut)
	for i := 0; i < 3; i++ {
		code, out, errOut = runAt(t, dir, "update", "a", "--section", "Body=old")
		requireOK(t, code, out, errOut)
	}
	code, out, errOut = runAt(t, dir, "update", "a", "--name", "renamed")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "create", "--name", "a", "--section", "Body=new")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "show", "a")
	requireOK(t, code, out, errOut)
	if !strings.Contains(out, "new") || strings.Contains(out, "old") {
		t.Fatalf("historical record won name lookup: %s", out)
	}
}

func TestSkillArtifactImportVerifyExportAndManagedForce(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAt(t, dir, "init")
	requireOK(t, code, out, errOut)
	source := filepath.Join(dir, "skill-source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runAt(t, dir, "--json", "skill", "import", source, "--name", "review")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "skill", "verify")
	requireOK(t, code, out, errOut)
	if !strings.Contains(out, "\"checked\": 1") {
		t.Fatalf("unexpected verification: %s", out)
	}
	code, out, errOut = runAt(t, dir, "--json", "skill", "import", source, "--name", "review")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--json", "skill", "list")
	requireOK(t, code, out, errOut)
	if !strings.Contains(out, "\"count\": 1") {
		t.Fatalf("identical import created a second current artifact: %s", out)
	}
	code, out, errOut = runAt(t, dir, "--json", "skill", "export", "review", "--target", "codex")
	requireOK(t, code, out, errOut)
	destination := filepath.Join(dir, ".agents", "skills", "review")
	if _, err := os.Stat(filepath.Join(destination, "SKILL.md")); err != nil {
		t.Fatalf("export did not restore skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, ".lbc-artifact.json"), []byte(`{"kind":"skill","name":"other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runAt(t, dir, "skill", "export", "review", "--target", "codex", "--force")
	if code == 0 {
		t.Fatal("force export replaced another artifact's managed destination")
	}
}

func TestVersionAndPrivateUpgradeJSONContracts(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"--json", "--version"}, &out, &errOut); code != 0 {
		t.Fatalf("version exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"runtime": "go"`) {
		t.Fatalf("unexpected version response: %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"--json", "upgrade"}, &out, &errOut); code != 1 {
		t.Fatalf("upgrade exit %d", code)
	}
	if !strings.Contains(out.String(), `"updated": false`) || strings.Contains(out.String(), `"error": "reported"`) {
		t.Fatalf("unexpected upgrade response: %s", out.String())
	}
}

func TestResolveMergesAtlasOnlyWhenEnabled(t *testing.T) {
	merge := true
	parent := model.Prompt{ID: "parent", Name: "parent", Version: 1, Status: "active", Atlas: &model.Atlas{Prime: &model.AtlasPrime{Domains: []string{"base"}}, OnEmpty: "warn"}}
	child := model.Prompt{ID: "child", Name: "child", Version: 1, Status: "active", Extends: "parent", ExtendsAtlas: &merge, Atlas: &model.Atlas{Prime: &model.AtlasPrime{Domains: []string{"child", "base"}}, OnEmpty: "error"}}
	resolved, err := resolve([]model.Prompt{parent, child}, "child", nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Atlas == nil || strings.Join(resolved.Atlas.Prime.Domains, ",") != "base,child" || resolved.Atlas.OnEmpty != "error" {
		t.Fatalf("unexpected merged atlas: %#v", resolved.Atlas)
	}
	merge = false
	child.ExtendsAtlas = &merge
	resolved, err = resolve([]model.Prompt{parent, child}, "child", nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(resolved.Atlas.Prime.Domains, ",") != "child,base" {
		t.Fatalf("atlas merged while disabled: %#v", resolved.Atlas)
	}
}

func TestCommandMigratesLegacyCanopyLayout(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".canopy")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.yaml"), []byte("project: legacy\nversion: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prompts.jsonl", "schemas.jsonl", "artifacts.jsonl"} {
		if err := os.WriteFile(filepath.Join(legacy, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := runAt(t, dir, "--json", "list")
	requireOK(t, code, out, errOut)
	if _, err := os.Stat(filepath.Join(dir, ".luuna", "beacon", "config.yaml")); err != nil {
		t.Fatalf("legacy layout was not migrated: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy directory remained after migration: %v", err)
	}
}

func TestCompletionsGenerateShellScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, errOut bytes.Buffer
		if code := Run([]string{"completions", shell}, &out, &errOut); code != 0 {
			t.Fatalf("%s completion failed: %s", shell, errOut.String())
		}
		if !strings.Contains(out.String(), "luuna-beacon") || !strings.Contains(out.String(), "create") {
			t.Fatalf("%s output incomplete: %s", shell, out.String())
		}
	}
}

func TestQuietSuppressesHumanRenderOutput(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAt(t, dir, "init")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "create", "--name", "quiet", "--section", "Task=hidden")
	requireOK(t, code, out, errOut)
	code, out, errOut = runAt(t, dir, "--quiet", "render", "quiet")
	requireOK(t, code, out, errOut)
	if out != "" {
		t.Fatalf("quiet render emitted output: %q", out)
	}
}
