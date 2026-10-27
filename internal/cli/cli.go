package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/luuna-ai/luuna-beacon/internal/artifact"
	"github.com/luuna-ai/luuna-beacon/internal/model"
	"github.com/luuna-ai/luuna-beacon/internal/paths"
	"github.com/luuna-ai/luuna-beacon/internal/store"
	"gopkg.in/yaml.v3"
)

const version = "0.2.6"

type context struct {
	cwd   string
	out   io.Writer
	err   io.Writer
	json  bool
	quiet bool
}
type reportedError struct{}

func (reportedError) Error() string { return "reported" }

func Run(args []string, out, errOut io.Writer) int {
	started := time.Now()
	args, jsonMode := takeFlag(args, "--json")
	args, quietLong := takeFlag(args, "--quiet")
	args, quietShort := takeFlag(args, "-q")
	args, timing := takeFlag(args, "--timing")
	if has(args, "--version") || has(args, "-v") {
		if jsonMode {
			writeJSON(out, map[string]any{"name": "@luuna/beacon-cli", "version": version, "runtime": "go", "platform": runtime.GOOS + "-" + runtime.GOARCH})
		} else {
			fmt.Fprintln(out, version)
		}
		return 0
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		help(out)
		return 0
	}
	if has(args[1:], "--help") || has(args[1:], "-h") {
		commandHelp(out, args)
		return 0
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		fmt.Fprintln(errOut, cwdErr)
		return 1
	}
	ctx := context{cwd: cwd, out: out, err: errOut, json: jsonMode, quiet: quietLong || quietShort}
	if runErr := dispatch(&ctx, args); runErr != nil {
		if _, alreadyReported := runErr.(reportedError); alreadyReported {
			return 1
		}
		if jsonMode {
			writeJSON(out, map[string]any{"success": false, "command": args[0], "error": runErr.Error()})
		} else {
			fmt.Fprintf(errOut, "Error: %v\n", runErr)
		}
		return 1
	}
	if timing {
		fmt.Fprintf(errOut, "[timing] %dms\n", time.Since(started).Milliseconds())
	}
	return 0
}

func dispatch(ctx *context, args []string) error {
	switch args[0] {
	case "init":
		return initProject(ctx)
	case "create":
		return create(ctx, args[1:])
	case "update":
		return update(ctx, args[1:])
	case "archive":
		return archive(ctx, args[1:])
	case "list":
		return list(ctx, args[1:])
	case "show":
		return show(ctx, args[1:])
	case "render":
		return render(ctx, args[1:])
	case "emit":
		return emit(ctx, args[1:])
	case "stats":
		return stats(ctx)
	case "tree":
		return tree(ctx, args[1:])
	case "pin":
		return pin(ctx, args[1:], true)
	case "unpin":
		return pin(ctx, args[1:], false)
	case "config":
		return configCommand(ctx, args[1:])
	case "schema":
		return schemaCommand(ctx, args[1:])
	case "skill", "agent", "bundle":
		return artifactCommand(ctx, args[0], args[1:])
	case "prime":
		return prime(ctx, args[1:])
	case "onboard":
		return onboard(ctx, args[1:])
	case "doctor":
		return doctor(ctx, args[1:])
	case "validate":
		return validate(ctx, args[1:])
	case "import":
		return importMarkdown(ctx, args[1:])
	case "diff":
		return diff(ctx, args[1:])
	case "sync":
		return sync(ctx, args[1:])
	case "upgrade":
		message := "Luuna Beacon is not published; update through a reviewed Luuna checkout."
		if ctx.json {
			writeJSON(ctx.out, map[string]any{"success": false, "command": "upgrade", "error": message, "updated": false})
		} else {
			fmt.Fprintln(ctx.err, message)
		}
		return reportedError{}
	case "history":
		return history(ctx, args[1:])
	case "completions":
		return completions(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func initProject(ctx *context) error {
	if err := paths.MigrateLegacy(ctx.cwd); err != nil {
		return err
	}
	dir := filepath.Join(ctx.cwd, ".luuna", "beacon")
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf(".luuna/beacon/ already exists")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	config := model.Config{Project: "luuna-beacon", Version: "1", Targets: map[string]model.EmitTarget{"default": {Dir: "agents", Default: true}}}
	encoded, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), encoded, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.lock\n"), 0o644); err != nil {
		return err
	}
	for _, name := range []string{"prompts.jsonl", "schemas.jsonl", "artifacts.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			return err
		}
	}
	attrsPath := filepath.Join(ctx.cwd, ".gitattributes")
	existing, readErr := os.ReadFile(attrsPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	additions := ""
	for _, record := range []string{"prompts.jsonl", "schemas.jsonl", "artifacts.jsonl"} {
		entry := ".luuna/beacon/" + record + " merge=union"
		if !strings.Contains(string(existing)+additions, entry) {
			additions += entry + "\n"
		}
	}
	if additions != "" {
		prefix := ""
		if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
			prefix = "\n"
		}
		if err := os.WriteFile(attrsPath, append(existing, []byte(prefix+additions)...), 0o644); err != nil {
			return err
		}
	}
	respond(ctx, map[string]any{"success": true, "command": "init", "dir": dir}, "Initialized .luuna/beacon")
	return nil
}

func create(ctx *context, args []string) error {
	name, ok := option(args, "--name")
	if !ok || name == "" {
		return fmt.Errorf("--name is required")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	active := current(records)
	for _, prompt := range active {
		if prompt.Name == name && prompt.Status != "archived" {
			return fmt.Errorf("prompt name %q already exists", name)
		}
	}
	parent := value(args, "--extends")
	if parent != "" {
		if _, err := find(active, parent); err != nil {
			return fmt.Errorf("parent prompt %q not found", parent)
		}
	}
	for _, mixin := range options(args, "--mixin") {
		if _, err := find(active, mixin); err != nil {
			return fmt.Errorf("mixin prompt %q not found", mixin)
		}
	}
	config, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	sections := parseSections(args)
	frontmatter := map[string]any{}
	for _, raw := range options(args, "--fm") {
		key, val, ok := strings.Cut(raw, "=")
		if ok && key != "" {
			frontmatter[key] = val
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	prompt := model.Prompt{ID: config.Project + "-" + fmt.Sprintf("%x", time.Now().UnixNano()), Name: name, Version: 1, Sections: sections, Description: value(args, "--description"), Extends: parent, Mixins: options(args, "--mixin"), Tags: options(args, "--tag"), Schema: value(args, "--schema"), EmitAs: value(args, "--emit-as"), EmitDir: value(args, "--emit-dir"), Status: defaultValue(value(args, "--status"), "active"), CreatedAt: now, UpdatedAt: now}
	if len(frontmatter) > 0 {
		prompt.Frontmatter = frontmatter
	}
	if prompt.Status != "active" && prompt.Status != "draft" {
		return fmt.Errorf("invalid status: %s", prompt.Status)
	}
	if err := store.AppendJSONL(path, prompt); err != nil {
		return err
	}
	respond(ctx, map[string]any{"success": true, "command": "create", "id": prompt.ID, "name": prompt.Name, "version": prompt.Version, "prompt": prompt}, "Created prompt "+name)
	return nil
}

func update(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	prompt, err := find(current(records), args[0])
	if err != nil {
		return err
	}
	next := prompt
	next.Version++
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if name := value(args, "--name"); name != "" {
		next.Name = name
	}
	if description, ok := option(args, "--description"); ok {
		next.Description = description
	}
	if status := value(args, "--status"); status != "" {
		if status != "active" && status != "draft" && status != "archived" {
			return fmt.Errorf("invalid status: %s", status)
		}
		next.Status = status
	}
	if extends, ok := option(args, "--extends"); ok {
		next.Extends = extends
	}
	if schema, ok := option(args, "--schema"); ok {
		next.Schema = schema
	}
	if emitAs, ok := option(args, "--emit-as"); ok {
		next.EmitAs = emitAs
	}
	if emitDir, ok := option(args, "--emit-dir"); ok {
		next.EmitDir = emitDir
	}
	for _, tag := range options(args, "--tag") {
		if !contains(next.Tags, tag) {
			next.Tags = append(next.Tags, tag)
		}
	}
	for _, tag := range options(args, "--untag") {
		next.Tags = without(next.Tags, tag)
	}
	sections := append(parseNamedSections(args, "--section"), parseNamedSections(args, "--add-section")...)
	for _, section := range sections {
		replaced := false
		for index := range next.Sections {
			if next.Sections[index].Name == section.Name {
				next.Sections[index].Body = section.Body
				replaced = true
			}
		}
		if !replaced {
			next.Sections = append(next.Sections, section)
		}
	}
	for _, name := range options(args, "--remove-section") {
		if next.Extends != "" {
			found := false
			for index := range next.Sections {
				if next.Sections[index].Name == name {
					next.Sections[index].Body = ""
					found = true
				}
			}
			if !found {
				next.Sections = append(next.Sections, model.Section{Name: name, Body: ""})
			}
		} else {
			kept := []model.Section{}
			for _, section := range next.Sections {
				if section.Name != name {
					kept = append(kept, section)
				}
			}
			next.Sections = kept
		}
	}
	for _, mixin := range options(args, "--mixin") {
		if !contains(next.Mixins, mixin) {
			next.Mixins = append(next.Mixins, mixin)
		}
	}
	for _, mixin := range options(args, "--remove-mixin") {
		next.Mixins = without(next.Mixins, mixin)
	}
	fm := map[string]any{}
	for key, entry := range next.Frontmatter {
		fm[key] = entry
	}
	for _, raw := range options(args, "--fm") {
		key, entry, ok := strings.Cut(raw, "=")
		if ok && key != "" {
			fm[key] = entry
		}
	}
	for _, key := range options(args, "--remove-fm") {
		delete(fm, key)
	}
	if len(fm) == 0 {
		next.Frontmatter = nil
	} else {
		next.Frontmatter = fm
	}
	if err := store.AppendJSONL(path, next); err != nil {
		return err
	}
	respond(ctx, map[string]any{"success": true, "command": "update", "name": next.Name, "version": next.Version, "prompt": next}, "Updated prompt "+next.Name)
	return nil
}

func archive(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	prompt, err := find(current(records), args[0])
	if err != nil {
		return err
	}
	if prompt.Status == "archived" {
		return fmt.Errorf("prompt %q is already archived", args[0])
	}
	prompt.Version++
	prompt.Status = "archived"
	prompt.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.AppendJSONL(path, prompt); err != nil {
		return err
	}
	respond(ctx, map[string]any{"success": true, "command": "archive", "name": prompt.Name, "version": prompt.Version}, "Archived prompt "+prompt.Name)
	return nil
}

func list(ctx *context, args []string) error {
	records, err := prompts(ctx)
	if err != nil {
		return err
	}
	status := value(args, "--status")
	result := []model.Prompt{}
	for _, prompt := range records {
		if status == "" && prompt.Status == "archived" {
			continue
		}
		if status != "" && prompt.Status != status {
			continue
		}
		if tag := value(args, "--tag"); tag != "" && !contains(prompt.Tags, tag) {
			continue
		}
		if parent := value(args, "--extends"); parent != "" && prompt.Extends != parent {
			continue
		}
		if mixin := value(args, "--mixin"); mixin != "" && !contains(prompt.Mixins, mixin) {
			continue
		}
		result = append(result, prompt)
	}
	respond(ctx, map[string]any{"success": true, "command": "list", "prompts": result, "count": len(result)}, fmt.Sprintf("%d prompt(s)", len(result)))
	return nil
}

func show(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	_, _, wantsVersion := strings.Cut(args[0], "@")
	lookup := current(records)
	if wantsVersion {
		lookup = records
	}
	prompt, err := find(lookup, args[0])
	if err != nil {
		return err
	}
	respond(ctx, map[string]any{"success": true, "command": "show", "prompt": prompt}, prompt.Name)
	return nil
}

func resolve(records []model.Prompt, reference string, version *int, visiting map[string]bool, depth int) (model.Prompt, error) {
	if depth > model.MaxInheritDepth {
		return model.Prompt{}, fmt.Errorf("maximum inheritance depth exceeded")
	}
	if visiting == nil {
		visiting = map[string]bool{}
	}
	name := strings.Split(reference, "@")[0]
	var chosen *model.Prompt
	for index := range records {
		candidate := &records[index]
		if candidate.Name != name {
			continue
		}
		if version != nil && candidate.Version != *version {
			continue
		}
		if chosen == nil || candidate.Version > chosen.Version {
			chosen = candidate
		}
	}
	if chosen == nil {
		return model.Prompt{}, fmt.Errorf("prompt %q not found", reference)
	}
	key := chosen.ID + fmt.Sprintf("@%d", chosen.Version)
	if visiting[key] {
		return model.Prompt{}, fmt.Errorf("circular inheritance involving %q", chosen.Name)
	}
	visiting[key] = true
	defer delete(visiting, key)
	result := *chosen
	sections := []model.Section{}
	frontmatter := map[string]any{}
	var inheritedAtlas *model.Atlas
	if chosen.Extends != "" {
		parent, err := resolve(records, chosen.Extends, nil, visiting, depth+1)
		if err != nil {
			return model.Prompt{}, err
		}
		sections = append(sections, parent.Sections...)
		for key, value := range parent.Frontmatter {
			frontmatter[key] = value
		}
		inheritedAtlas = parent.Atlas
	}
	for _, mixinName := range chosen.Mixins {
		mixin, err := resolve(records, mixinName, nil, visiting, depth+1)
		if err != nil {
			return model.Prompt{}, err
		}
		sections = mergeSections(sections, mixin.Sections)
		for key, value := range mixin.Frontmatter {
			frontmatter[key] = value
		}
		if chosen.ExtendsAtlas != nil && *chosen.ExtendsAtlas {
			inheritedAtlas = mergeAtlas(inheritedAtlas, mixin.Atlas)
		}
	}
	result.Sections = mergeSections(sections, chosen.Sections)
	for key, value := range chosen.Frontmatter {
		frontmatter[key] = value
	}
	if len(frontmatter) > 0 {
		result.Frontmatter = frontmatter
	}
	if chosen.ExtendsAtlas != nil && *chosen.ExtendsAtlas {
		result.Atlas = mergeAtlas(inheritedAtlas, chosen.Atlas)
	}
	return result, nil
}
func mergeAtlas(base, overlay *model.Atlas) *model.Atlas {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	result := &model.Atlas{}
	if base.Prime != nil || overlay.Prime != nil {
		result.Prime = &model.AtlasPrime{}
		seenDomains, seenFiles := map[string]bool{}, map[string]bool{}
		for _, atlas := range []*model.Atlas{base, overlay} {
			if atlas.Prime != nil {
				for _, domain := range atlas.Prime.Domains {
					if !seenDomains[domain] {
						result.Prime.Domains = append(result.Prime.Domains, domain)
						seenDomains[domain] = true
					}
				}
				for _, file := range atlas.Prime.Files {
					if !seenFiles[file] {
						result.Prime.Files = append(result.Prime.Files, file)
						seenFiles[file] = true
					}
				}
			}
		}
	}
	result.Budget = base.Budget
	if overlay.Budget != nil {
		result.Budget = overlay.Budget
	}
	result.OnEmpty = base.OnEmpty
	if overlay.OnEmpty != "" {
		result.OnEmpty = overlay.OnEmpty
	}
	return result
}
func mergeSections(base, overlay []model.Section) []model.Section {
	result := append([]model.Section{}, base...)
	for _, replacement := range overlay {
		found := -1
		for index, current := range result {
			if current.Name == replacement.Name {
				found = index
			}
		}
		if replacement.Body == "" {
			if found >= 0 {
				result = append(result[:found], result[found+1:]...)
			}
			continue
		}
		if found >= 0 {
			result[found] = replacement
		} else {
			result = append(result, replacement)
		}
	}
	return result
}
func history(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	items := []model.Prompt{}
	for _, item := range records {
		if item.Name == strings.Split(args[0], "@")[0] {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version > items[j].Version })
	limit := 20
	if raw := value(args, "--limit"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil || limit < 0 {
			return fmt.Errorf("invalid limit: %s", raw)
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	if len(items) == 0 {
		return fmt.Errorf("prompt %q not found", args[0])
	}
	respond(ctx, map[string]any{"success": true, "command": "history", "name": args[0], "versions": items}, fmt.Sprintf("%d version(s)", len(items)))
	return nil
}
func render(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	records, err := prompts(ctx)
	if err != nil {
		return err
	}
	name, versionText, wantsVersion := strings.Cut(args[0], "@")
	var wanted *int
	if wantsVersion {
		parsed := 0
		if _, err := fmt.Sscanf(versionText, "%d", &parsed); err != nil {
			return fmt.Errorf("invalid version: %s", versionText)
		}
		wanted = &parsed
	}
	prompt, err := resolve(records, name, wanted, nil, 0)
	if err != nil {
		return err
	}
	if value(args, "--format") == "json" {
		writeJSON(ctx.out, prompt)
		return nil
	}
	payload := map[string]any{"success": true, "command": "render", "name": prompt.Name, "version": prompt.Version, "sections": prompt.Sections, "frontmatter": prompt.Frontmatter}
	if prompt.Atlas != nil {
		payload["atlas"] = prompt.Atlas
	}
	if ctx.json {
		writeJSON(ctx.out, payload)
	} else if !ctx.quiet {
		fmt.Fprint(ctx.out, renderMarkdown(prompt))
	}
	return nil
}

func emit(ctx *context, args []string) error {
	records, err := prompts(ctx)
	if err != nil {
		return err
	}
	all := has(args, "--all") || has(args, "--check")
	selected := []model.Prompt{}
	if all {
		for _, prompt := range records {
			if prompt.Status == "active" {
				selected = append(selected, prompt)
			}
		}
	} else {
		if len(args) == 0 {
			return fmt.Errorf("prompt name or --all required")
		}
		prompt, findErr := find(records, args[0])
		if findErr != nil {
			return findErr
		}
		selected = append(selected, prompt)
	}
	config, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	files := []map[string]any{}
	stale := []string{}
	for _, prompt := range selected {
		resolved, resolveErr := resolve(records, prompt.Name, prompt.Pinned, nil, 0)
		if resolveErr != nil {
			return resolveErr
		}
		filename := prompt.EmitAs
		if filename == "" {
			filename = prompt.Name + ".md"
		}
		out := value(args, "--out")
		if out == "" {
			directory := value(args, "--out-dir")
			if directory == "" {
				directory = emitDir(prompt, config)
			}
			out = filepath.Join(ctx.cwd, directory, filename)
		}
		content := renderMarkdown(resolved)
		if has(args, "--check") {
			actual, readErr := os.ReadFile(out)
			if readErr != nil || string(actual) != content {
				stale = append(stale, prompt.Name)
			}
			continue
		}
		if has(args, "--dry-run") {
			files = append(files, map[string]any{"name": prompt.Name, "path": out, "version": resolved.Version})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if actual, readErr := os.ReadFile(out); readErr == nil && string(actual) == content && !has(args, "--force") {
			files = append(files, map[string]any{"name": prompt.Name, "path": out, "version": resolved.Version, "skipped": true})
			continue
		}
		if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
			return err
		}
		files = append(files, map[string]any{"name": prompt.Name, "path": out, "version": resolved.Version})
	}
	if has(args, "--check") {
		success := len(stale) == 0
		respond(ctx, map[string]any{"success": success, "command": "emit", "check": true, "stale": stale, "upToDate": success}, fmt.Sprintf("%d stale file(s)", len(stale)))
		if !success {
			return fmt.Errorf("%d stale file(s)", len(stale))
		}
		return nil
	}
	respond(ctx, map[string]any{"success": true, "command": "emit", "files": files, "count": len(files)}, fmt.Sprintf("Emitted %d file(s)", len(files)))
	return nil
}

func stats(ctx *context) error {
	prompts, err := prompts(ctx)
	if err != nil {
		return err
	}
	byStatus := map[string]int{}
	byTag := map[string]int{}
	for _, prompt := range prompts {
		byStatus[prompt.Status]++
		for _, tag := range prompt.Tags {
			byTag[tag]++
		}
	}
	respond(ctx, map[string]any{"success": true, "command": "stats", "prompts": map[string]any{"total": len(prompts), "byStatus": byStatus, "byTag": byTag}}, fmt.Sprintf("%d prompt(s)", len(prompts)))
	return nil
}

func tree(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	prompts, err := prompts(ctx)
	if err != nil {
		return err
	}
	prompt, err := find(prompts, args[0])
	if err != nil {
		return err
	}
	ancestors := []string{}
	parent := prompt.Extends
	for parent != "" {
		ancestors = append(ancestors, parent)
		next, findErr := find(prompts, parent)
		if findErr != nil {
			break
		}
		parent = next.Extends
	}
	children := []string{}
	mixinUsers := []string{}
	for _, candidate := range prompts {
		if candidate.Extends == prompt.Name {
			children = append(children, candidate.Name)
		}
		if contains(candidate.Mixins, prompt.Name) {
			mixinUsers = append(mixinUsers, candidate.Name)
		}
	}
	respond(ctx, map[string]any{"success": true, "command": "tree", "name": prompt.Name, "ancestors": ancestors, "mixins": prompt.Mixins, "tree": map[string]any{"name": prompt.Name, "children": children, "mixinUsers": mixinUsers}}, prompt.Name)
	return nil
}

func pin(ctx *context, args []string, set bool) error {
	if len(args) == 0 {
		return fmt.Errorf("prompt name required")
	}
	name, rawVersion, hasVersion := strings.Cut(args[0], "@")
	if set && !hasVersion {
		return fmt.Errorf("expected name@version")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	prompt, err := find(current(records), name)
	if err != nil {
		return err
	}
	if set {
		var wanted int
		if _, err := fmt.Sscanf(rawVersion, "%d", &wanted); err != nil {
			return fmt.Errorf("invalid version: %s", rawVersion)
		}
		exists := false
		for _, record := range records {
			if record.ID == prompt.ID && record.Version == wanted {
				exists = true
			}
		}
		if !exists {
			return fmt.Errorf("version %d not found", wanted)
		}
		prompt.Pinned = &wanted
	} else {
		prompt.Pinned = nil
	}
	prompt.Version++
	prompt.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.AppendJSONL(path, prompt); err != nil {
		return err
	}
	command := "unpin"
	if set {
		command = "pin"
	}
	respond(ctx, map[string]any{"success": true, "command": command, "name": prompt.Name, "pinned": prompt.Pinned}, command+"ned "+prompt.Name)
	return nil
}

func configCommand(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: luuna-beacon config <schema|show|set|unset>")
	}
	if args[0] == "schema" {
		writeJSON(ctx.out, map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "required": []string{"project", "version"}, "properties": map[string]any{"project": map[string]any{"type": "string", "minLength": 1}, "version": map[string]any{"type": "string", "minLength": 1}, "targets": map[string]any{"type": "object"}}, "additionalProperties": false})
		return nil
	}
	path, err := paths.Config(ctx.cwd)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return err
	}
	switch args[0] {
	case "show":
		if dotted := value(args, "--path"); dotted != "" {
			found, ok := getPath(raw, dotted)
			if !ok {
				return fmt.Errorf("path not found: %s", dotted)
			}
			if ctx.json {
				respond(ctx, map[string]any{"success": true, "command": "config show", "path": dotted, "value": found}, "")
			} else {
				encoded, _ := yaml.Marshal(found)
				fmt.Fprint(ctx.out, string(encoded))
			}
			return nil
		}
		if ctx.json {
			respond(ctx, map[string]any{"success": true, "command": "config show", "config": raw}, "")
		} else {
			fmt.Fprint(ctx.out, string(content))
		}
		return nil
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("config path and value required")
		}
		var replacement any
		if err := yaml.Unmarshal([]byte(args[2]), &replacement); err != nil {
			return err
		}
		if err := setPath(raw, args[1], replacement); err != nil {
			return err
		}
		if err := validateRawConfig(raw); err != nil {
			return err
		}
		if err := saveRawConfig(path, raw); err != nil {
			return err
		}
		respond(ctx, map[string]any{"success": true, "command": "config set", "path": args[1], "value": replacement}, "Set "+args[1])
		return nil
	case "unset":
		if len(args) < 2 {
			return fmt.Errorf("config path required")
		}
		removed := unsetPath(raw, args[1])
		if removed {
			if err := validateRawConfig(raw); err != nil {
				return err
			}
			if err := saveRawConfig(path, raw); err != nil {
				return err
			}
		}
		respond(ctx, map[string]any{"success": true, "command": "config unset", "path": args[1], "removed": removed}, "Unset "+args[1])
		return nil
	default:
		return fmt.Errorf("usage: luuna-beacon config <schema|show|set|unset>")
	}
}

func schemaCommand(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: luuna-beacon schema <create|show|list|rule add>")
	}
	path, err := paths.Schemas(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Schema](path)
	if err != nil {
		return err
	}
	currentSchemas := func() []model.Schema {
		byID := map[string]model.Schema{}
		for _, schema := range records {
			if prior, ok := byID[schema.ID]; !ok || schema.UpdatedAt > prior.UpdatedAt {
				byID[schema.ID] = schema
			}
		}
		result := []model.Schema{}
		for _, schema := range byID {
			result = append(result, schema)
		}
		return result
	}
	switch args[0] {
	case "create":
		name := value(args, "--name")
		if name == "" {
			return fmt.Errorf("--name is required")
		}
		for _, schema := range currentSchemas() {
			if schema.Name == name {
				return fmt.Errorf("schema name %q already exists", name)
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		required := []string{}
		if raw := value(args, "--required"); raw != "" {
			required = strings.Split(raw, ",")
		}
		schema := model.Schema{ID: "schema-" + fmt.Sprintf("%x", time.Now().UnixNano()), Name: name, RequiredSections: required, OptionalSections: strings.Split(strings.Trim(value(args, "--optional"), ","), ","), CreatedAt: now, UpdatedAt: now}
		if len(schema.OptionalSections) == 1 && schema.OptionalSections[0] == "" {
			schema.OptionalSections = nil
		}
		if err := store.AppendJSONL(path, schema); err != nil {
			return err
		}
		respond(ctx, map[string]any{"success": true, "command": "schema create", "id": schema.ID, "name": schema.Name}, "Created schema "+name)
		return nil
	case "list":
		schemas := currentSchemas()
		respond(ctx, map[string]any{"success": true, "command": "schema list", "schemas": schemas, "count": len(schemas)}, fmt.Sprintf("%d schema(s)", len(schemas)))
		return nil
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("schema name required")
		}
		for _, schema := range currentSchemas() {
			if schema.Name == args[1] {
				respond(ctx, map[string]any{"success": true, "command": "schema show", "schema": schema}, schema.Name)
				return nil
			}
		}
		return fmt.Errorf("schema %q not found", args[1])
	case "rule":
		if len(args) < 3 || args[1] != "add" {
			return fmt.Errorf("usage: luuna-beacon schema rule add <schema> --section <name> --pattern <regex> --message <text>")
		}
		section, pattern, message := value(args, "--section"), value(args, "--pattern"), value(args, "--message")
		if section == "" || pattern == "" || message == "" {
			return fmt.Errorf("--section, --pattern, and --message are required")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid rule pattern: %w", err)
		}
		for _, schema := range currentSchemas() {
			if schema.Name != args[2] {
				continue
			}
			schema.Rules = append(schema.Rules, model.Rule{Section: section, Pattern: pattern, Message: message})
			schema.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := store.AppendJSONL(path, schema); err != nil {
				return err
			}
			respond(ctx, map[string]any{"success": true, "command": "schema rule add", "schema": schema.Name, "rule": schema.Rules[len(schema.Rules)-1]}, "Added rule to "+schema.Name)
			return nil
		}
		return fmt.Errorf("schema %q not found", args[2])
	}
	return fmt.Errorf("usage: luuna-beacon schema <create|show|list|rule add>")
}

func artifactCommand(ctx *context, kind string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("artifact subcommand required")
	}
	path, err := paths.Artifacts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Artifact](path)
	if err != nil {
		return err
	}
	seenVersions := map[string]string{}
	for _, record := range records {
		if err := artifact.Verify(record); err != nil {
			return fmt.Errorf("invalid artifact record %s %s@%d: %w", record.Kind, record.Name, record.Version, err)
		}
		key := record.Kind + "\x00" + record.Name + "\x00" + fmt.Sprint(record.Version)
		if previous, exists := seenVersions[key]; exists && previous != record.SHA256 {
			return fmt.Errorf("conflicting artifact version: %s %s@%d", record.Kind, record.Name, record.Version)
		}
		seenVersions[key] = record.SHA256
	}
	selectRecord := func(reference string) (model.Artifact, error) {
		name, rawVersion, wantsVersion := strings.Cut(reference, "@")
		var selected *model.Artifact
		for index := range records {
			candidate := &records[index]
			if candidate.Kind != kind || candidate.Name != name {
				continue
			}
			if wantsVersion {
				var wanted int
				if _, err := fmt.Sscanf(rawVersion, "%d", &wanted); err != nil || candidate.Version != wanted {
					continue
				}
			}
			if selected == nil || candidate.Version > selected.Version {
				selected = candidate
			}
		}
		if selected == nil {
			return model.Artifact{}, fmt.Errorf("%s %q not found", kind, reference)
		}
		return *selected, nil
	}
	switch args[0] {
	case "import":
		if len(args) < 2 {
			return fmt.Errorf("source path required")
		}
		name := value(args, "--name")
		if name == "" {
			return fmt.Errorf("--name is required")
		}
		files, err := artifact.Snapshot(kind, name, args[1])
		if err != nil {
			return err
		}
		version := 1
		var previous *model.Artifact
		for _, record := range records {
			if record.Kind == kind && record.Name == name && record.Version >= version {
				version = record.Version + 1
				copy := record
				previous = &copy
			}
		}
		next := artifact.New(kind, name, files, version)
		if previous != nil && previous.SHA256 == next.SHA256 {
			respond(ctx, map[string]any{"success": true, "command": kind + " import", "artifact": previous}, "Stored "+kind+" "+name)
			return nil
		}
		if previous != nil {
			next.ID = previous.ID
		}
		if err := store.AppendJSONL(path, next); err != nil {
			return err
		}
		respond(ctx, map[string]any{"success": true, "command": kind + " import", "artifact": next}, "Stored "+kind+" "+name)
		return nil
	case "update":
		if len(args) < 3 {
			return fmt.Errorf("name and source path required")
		}
		files, err := artifact.Snapshot(kind, args[1], args[2])
		if err != nil {
			return err
		}
		version := 1
		var previous *model.Artifact
		for _, record := range records {
			if record.Kind == kind && record.Name == args[1] && record.Version >= version {
				version = record.Version + 1
				copy := record
				previous = &copy
			}
		}
		next := artifact.New(kind, args[1], files, version)
		if previous != nil && previous.SHA256 == next.SHA256 {
			respond(ctx, map[string]any{"success": true, "command": kind + " update", "artifact": previous}, "Updated "+kind+" "+args[1])
			return nil
		}
		if previous != nil {
			next.ID = previous.ID
		}
		if err := store.AppendJSONL(path, next); err != nil {
			return err
		}
		respond(ctx, map[string]any{"success": true, "command": kind + " update", "artifact": next}, "Updated "+kind+" "+args[1])
		return nil
	case "list":
		latest := map[string]model.Artifact{}
		for _, record := range records {
			if record.Kind != kind {
				continue
			}
			if prior, ok := latest[record.Name]; !ok || record.Version > prior.Version {
				latest[record.Name] = record
			}
		}
		items := make([]model.Artifact, 0, len(latest))
		for _, record := range latest {
			items = append(items, record)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		respond(ctx, map[string]any{"success": true, "command": kind + " list", "artifacts": items, "count": len(items)}, fmt.Sprintf("%d %s artifact(s)", len(items), kind))
		return nil
	case "verify":
		selected := []model.Artifact{}
		if len(args) > 1 {
			record, err := selectRecord(args[1])
			if err != nil {
				return err
			}
			selected = append(selected, record)
		} else {
			for _, record := range records {
				if record.Kind == kind {
					selected = append(selected, record)
				}
			}
		}
		for _, record := range selected {
			if err := artifact.Verify(record); err != nil {
				return err
			}
		}
		respond(ctx, map[string]any{"success": true, "command": kind + " verify", "checked": len(selected)}, fmt.Sprintf("Verified %d %s version(s)", len(selected), kind))
		return nil
	case "show", "history", "read", "export":
		if len(args) < 2 {
			return fmt.Errorf("artifact name required")
		}
		record, err := selectRecord(args[1])
		if err != nil {
			return err
		}
		switch args[0] {
		case "show":
			respond(ctx, map[string]any{"success": true, "command": kind + " show", "artifact": record}, record.Name)
			return nil
		case "history":
			versions := []model.Artifact{}
			for _, candidate := range records {
				if candidate.Kind == kind && candidate.Name == record.Name {
					versions = append(versions, candidate)
				}
			}
			sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
			respond(ctx, map[string]any{"success": true, "command": kind + " history", "name": record.Name, "versions": versions}, fmt.Sprintf("%d version(s)", len(versions)))
			return nil
		case "read":
			if len(args) < 3 {
				return fmt.Errorf("file path required")
			}
			for _, file := range record.Files {
				if file.Path == args[2] {
					respond(ctx, map[string]any{"success": true, "command": kind + " read", "name": record.Name, "version": record.Version, "file": file}, file.Content)
					return nil
				}
			}
			return fmt.Errorf("file %q not found", args[2])
		case "export":
			return exportArtifact(ctx, record, args)
		}
	}
	return fmt.Errorf("unknown %s subcommand: %s", kind, args[0])
}

func exportArtifact(ctx *context, record model.Artifact, args []string) error {
	destination := value(args, "--out")
	if destination == "" {
		target := value(args, "--target")
		switch {
		case target == "claude" && record.Kind == "skill":
			destination = filepath.Join(ctx.cwd, ".claude", "skills", record.Name)
		case target == "codex" && record.Kind == "skill":
			destination = filepath.Join(ctx.cwd, ".agents", "skills", record.Name)
		case target == "claude" && record.Kind == "agent":
			destination = filepath.Join(ctx.cwd, ".claude", "agents", record.Name+".md")
		default:
			return fmt.Errorf("--out is required for this export")
		}
	}
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(ctx.cwd, destination)
	}
	destination = filepath.Clean(destination)
	root, err := filepath.Abs(ctx.cwd)
	if err != nil {
		return err
	}
	dataRoot := filepath.Join(root, ".luuna")
	if destination == root || destination == dataRoot || strings.HasPrefix(destination, dataRoot+string(filepath.Separator)) {
		return fmt.Errorf("refusing to export over a project or Beacon data directory")
	}
	singleAgent := record.Kind == "agent" && strings.HasSuffix(strings.ToLower(destination), ".md")
	if singleAgent {
		if _, err := os.Stat(destination); err == nil && !has(args, "--force") {
			return fmt.Errorf("destination already exists: %s", destination)
		}
		if len(record.Files) != 1 {
			return fmt.Errorf("agent export requires exactly one stored file")
		}
		data, err := artifact.Decode(record.Files[0])
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		stage := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".lbc-stage-"+fmt.Sprintf("%x", time.Now().UnixNano()))
		if err := os.WriteFile(stage, data, 0o644); err != nil {
			return err
		}
		defer os.Remove(stage)
		backup := stage + ".backup"
		if _, err := os.Stat(destination); err == nil {
			if err := os.Rename(destination, backup); err != nil {
				return err
			}
		}
		if err := os.Rename(stage, destination); err != nil {
			_ = os.Rename(backup, destination)
			return err
		}
		_ = os.Remove(backup)
		respond(ctx, map[string]any{"success": true, "command": record.Kind + " export", "name": record.Name, "version": record.Version, "path": destination}, "Exported "+record.Name)
		return nil
	}
	backup := ""
	if _, err := os.Stat(destination); err == nil {
		if !has(args, "--force") {
			return fmt.Errorf("destination already exists: %s", destination)
		}
		marker, readErr := os.ReadFile(filepath.Join(destination, ".lbc-artifact.json"))
		var owner map[string]string
		if readErr != nil || json.Unmarshal(marker, &owner) != nil || owner["kind"] != record.Kind || owner["name"] != record.Name {
			return fmt.Errorf("refusing to replace an export not managed by %s %q", record.Kind, record.Name)
		}
		backup = destination + ".lbc-backup-" + fmt.Sprintf("%x", time.Now().UnixNano())
	}
	stage := destination + ".lbc-stage-" + fmt.Sprintf("%x", time.Now().UnixNano())
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, file := range record.Files {
		data, err := artifact.Decode(file)
		if err != nil {
			return err
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		if file.Executable {
			_ = os.Chmod(path, 0o755)
		}
	}
	marker, _ := json.Marshal(map[string]string{"kind": record.Kind, "name": record.Name})
	_ = os.WriteFile(filepath.Join(stage, ".lbc-artifact.json"), marker, 0o644)
	if backup != "" {
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, destination); err != nil {
		if backup != "" {
			_ = os.Rename(backup, destination)
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	respond(ctx, map[string]any{"success": true, "command": record.Kind + " export", "name": record.Name, "version": record.Version, "path": destination}, "Exported "+record.Name)
	return nil
}

func prime(ctx *context, args []string) error {
	content := "# Luuna Beacon Workflow Context\n\n- Storage: `.luuna/beacon/prompts.jsonl`\n- Use `lbc update`, then `lbc emit`; do not edit emitted files.\n- Run `lbc render <name>` to resolve composition.\n"
	if has(args, "--compact") {
		content = "# Luuna Beacon Quick Reference\n\nlbc list\nlbc render <name>\nlbc emit --all\nlbc update <name>\n"
	}
	if !has(args, "--export") {
		if dir, err := paths.BeaconDir(ctx.cwd); err == nil {
			if custom, readErr := os.ReadFile(filepath.Join(dir, "PRIME.md")); readErr == nil {
				content = string(custom)
			}
		}
	}
	if ctx.json {
		respond(ctx, map[string]any{"success": true, "command": "prime", "content": content}, "")
	} else if !ctx.quiet {
		fmt.Fprint(ctx.out, content)
	}
	return nil
}
func onboard(ctx *context, args []string) error {
	target := ""
	for _, candidate := range []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md"), "AGENTS.md"} {
		path := filepath.Join(ctx.cwd, candidate)
		if _, err := os.Stat(path); err == nil {
			target = path
			break
		}
	}
	if target == "" {
		target = filepath.Join(ctx.cwd, "CLAUDE.md")
	}
	start, end, versionMarker := "<!-- luuna-beacon:start -->", "<!-- luuna-beacon:end -->", "<!-- luuna-beacon-onboard-v:3 -->"
	snippet := start + "\n## Prompt Management (Luuna Beacon)\n" + versionMarker + "\n\nRun `lbc prime` at the start of every session. Use `lbc update` then `lbc emit`; do not edit emitted files.\n" + end + "\n"
	old, _ := os.ReadFile(target)
	if has(args, "--stdout") {
		if !ctx.quiet {
			fmt.Fprint(ctx.out, snippet)
		}
		return nil
	}
	if has(args, "--check") {
		status := "missing"
		if strings.Contains(string(old), start) && strings.Contains(string(old), end) {
			status = "outdated"
			if strings.Contains(string(old), versionMarker) {
				status = "current"
			}
		}
		respond(ctx, map[string]any{"success": true, "command": "onboard", "status": status, "file": target}, status)
		return nil
	}
	if startAt, endAt := strings.Index(string(old), start), strings.Index(string(old), end); startAt >= 0 && endAt > startAt {
		if strings.Contains(string(old)[startAt:endAt], versionMarker) {
			respond(ctx, map[string]any{"success": true, "command": "onboard", "action": "unchanged", "file": target}, "Current "+target)
			return nil
		}
		updated := string(old)[:startAt] + snippet + string(old)[endAt+len(end):]
		if err := os.WriteFile(target, []byte(updated), 0o644); err != nil {
			return err
		}
		respond(ctx, map[string]any{"success": true, "command": "onboard", "action": "updated", "file": target}, "Updated "+target)
		return nil
	}
	separator := ""
	if len(old) > 0 {
		separator = "\n"
		if !strings.HasSuffix(string(old), "\n") {
			separator = "\n\n"
		}
	}
	if err := os.WriteFile(target, append(old, []byte(separator+snippet)...), 0o644); err != nil {
		return err
	}
	action := "appended"
	if len(old) == 0 {
		action = "created"
	}
	respond(ctx, map[string]any{"success": true, "command": "onboard", "action": action, "file": target}, "Updated "+target)
	return nil
}
func doctor(ctx *context, args []string) error {
	dir, err := paths.BeaconDir(ctx.cwd)
	if err != nil {
		return err
	}
	checks := []map[string]any{}
	add := func(name, status, message string, details []string, fixable bool) {
		checks = append(checks, map[string]any{"name": name, "status": status, "message": message, "details": details, "fixable": fixable})
	}
	if _, err := loadConfig(ctx); err != nil {
		add("config", "fail", "Config is invalid", []string{err.Error()}, false)
	} else {
		add("config", "pass", "Config is valid", nil, false)
	}
	promptPath := filepath.Join(dir, "prompts.jsonl")
	records, promptErr := store.ReadJSONL[model.Prompt](promptPath)
	if promptErr != nil {
		add("prompts-integrity", "fail", "Prompt store is invalid", []string{promptErr.Error()}, false)
	} else {
		add("prompts-integrity", "pass", fmt.Sprintf("%d records, all valid", len(records)), nil, false)
	}
	schemaPath := filepath.Join(dir, "schemas.jsonl")
	schemas, schemaErr := store.ReadJSONL[model.Schema](schemaPath)
	if schemaErr != nil {
		add("schemas-integrity", "fail", "Schema store is invalid", []string{schemaErr.Error()}, false)
	} else {
		add("schemas-integrity", "pass", fmt.Sprintf("%d records, all valid", len(schemas)), nil, false)
	}
	artifactPath := filepath.Join(dir, "artifacts.jsonl")
	artifacts, artifactErr := store.ReadJSONL[model.Artifact](artifactPath)
	if artifactErr != nil {
		add("artifacts-integrity", "fail", "Artifact store is invalid", []string{artifactErr.Error()}, false)
	} else {
		details := []string{}
		for _, record := range artifacts {
			if err := artifact.Verify(record); err != nil {
				details = append(details, err.Error())
			}
		}
		if len(details) > 0 {
			add("artifacts-integrity", "fail", "Artifact store is invalid", details, false)
		} else {
			add("artifacts-integrity", "pass", fmt.Sprintf("%d stored artifact version(s) verified", len(artifacts)), nil, false)
		}
	}
	if promptErr == nil {
		details := []string{}
		for _, prompt := range current(records) {
			if prompt.Extends != "" || len(prompt.Mixins) > 0 {
				if _, err := resolve(current(records), prompt.Name, nil, nil, 0); err != nil {
					details = append(details, prompt.Name+": "+err.Error())
				}
			}
		}
		if len(details) > 0 {
			add("inheritance", "fail", "Broken inheritance references", details, false)
		} else {
			add("inheritance", "pass", "No broken references", nil, false)
		}
	}
	stale := []string{}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".lock") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 30*time.Second {
				stale = append(stale, entry.Name())
			}
		}
	}
	if has(args, "--fix") {
		for _, name := range stale {
			_ = os.Remove(filepath.Join(dir, name))
		}
		stale = nil
	}
	if len(stale) > 0 {
		add("stale-locks", "warn", fmt.Sprintf("%d stale lock file(s)", len(stale)), stale, true)
	} else {
		add("stale-locks", "pass", "No stale locks", nil, false)
	}
	failures, warnings := 0, 0
	for _, check := range checks {
		if check["status"] == "fail" {
			failures++
		}
		if check["status"] == "warn" {
			warnings++
		}
	}
	success := failures == 0
	response := map[string]any{"success": success, "command": "doctor", "checks": checks, "summary": map[string]int{"pass": len(checks) - failures - warnings, "warn": warnings, "fail": failures}}
	respond(ctx, response, fmt.Sprintf("%d passed, %d warning, %d failed", len(checks)-failures-warnings, warnings, failures))
	if !success {
		return fmt.Errorf("doctor found failures")
	}
	return nil
}
func validate(ctx *context, args []string) error {
	if len(args) == 0 && !has(args, "--all") {
		return fmt.Errorf("prompt name or --all required")
	}
	promptPath, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](promptPath)
	if err != nil {
		return err
	}
	schemaPath, err := paths.Schemas(ctx.cwd)
	if err != nil {
		return err
	}
	schemas, err := store.ReadJSONL[model.Schema](schemaPath)
	if err != nil {
		return err
	}
	selected := []model.Prompt{}
	if has(args, "--all") {
		for _, prompt := range current(records) {
			if prompt.Schema != "" {
				selected = append(selected, prompt)
			}
		}
	} else {
		prompt, err := resolve(records, args[0], nil, nil, 0)
		if err != nil {
			return err
		}
		selected = append(selected, prompt)
	}
	results := []map[string]any{}
	valid := true
	for _, prompt := range selected {
		errors := []string{}
		for _, schema := range schemas {
			if schema.Name != prompt.Schema {
				continue
			}
			for _, required := range schema.RequiredSections {
				found := false
				for _, section := range prompt.Sections {
					if section.Name == required {
						found = true
					}
				}
				if !found {
					errors = append(errors, "Required section \""+required+"\" is missing")
				}
			}
			for _, rule := range schema.Rules {
				for _, section := range prompt.Sections {
					if section.Name == rule.Section {
						if expression, compileErr := regexp.Compile(rule.Pattern); compileErr == nil && !expression.MatchString(section.Body) {
							errors = append(errors, rule.Message)
						}
					}
				}
			}
		}
		ok := len(errors) == 0
		if !ok {
			valid = false
		}
		results = append(results, map[string]any{"name": prompt.Name, "valid": ok, "errors": errors, "warnings": []string{}})
	}
	respond(ctx, map[string]any{"success": valid, "command": "validate", "results": results}, fmt.Sprintf("%d prompt(s) validated", len(results)))
	if !valid {
		return fmt.Errorf("validation failed")
	}
	return nil
}
func importMarkdown(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("markdown path required")
	}
	name := value(args, "--name")
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	content, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	body := string(content)
	frontmatter := map[string]any{}
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---\n"); end >= 0 {
			frontmatterText := body[4 : end+4]
			if err := yaml.Unmarshal([]byte(frontmatterText), &frontmatter); err != nil {
				return fmt.Errorf("invalid frontmatter: %w", err)
			}
			body = body[end+9:]
		}
	}
	sections := []model.Section{}
	sectionIndex := -1
	if has(args, "--no-split") {
		sections = append(sections, model.Section{Name: "body", Body: strings.TrimSpace(body)})
	} else {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "## ") {
				sections = append(sections, model.Section{Name: strings.TrimSpace(strings.TrimPrefix(line, "## "))})
				sectionIndex = len(sections) - 1
				continue
			}
			if sectionIndex >= 0 {
				if sections[sectionIndex].Body != "" {
					sections[sectionIndex].Body += "\n"
				}
				sections[sectionIndex].Body += line
			}
		}
	}
	if len(sections) == 0 {
		sections = []model.Section{{Name: "body", Body: strings.TrimSpace(body)}}
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	for _, prompt := range current(records) {
		if prompt.Name == name && prompt.Status != "archived" {
			return fmt.Errorf("prompt name %q already exists", name)
		}
	}
	config, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	prompt := model.Prompt{ID: config.Project + "-" + fmt.Sprintf("%x", time.Now().UnixNano()), Name: name, Version: 1, Sections: sections, Tags: options(args, "--tag"), Status: "active", CreatedAt: now, UpdatedAt: now}
	if description, ok := frontmatter["description"].(string); ok {
		prompt.Description = description
	}
	delete(frontmatter, "name")
	delete(frontmatter, "description")
	if len(frontmatter) > 0 {
		prompt.Frontmatter = frontmatter
	}
	if err := store.AppendJSONL(path, prompt); err != nil {
		return err
	}
	respond(ctx, map[string]any{"success": true, "command": "import", "id": prompt.ID, "name": prompt.Name, "sections": len(prompt.Sections)}, "Imported "+prompt.Name)
	return nil
}
func diff(ctx *context, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("name and two versions required")
	}
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return err
	}
	var first, second *model.Prompt
	for index := range records {
		record := &records[index]
		if record.Name != args[0] {
			continue
		}
		var version int
		fmt.Sscanf(args[1], "%d", &version)
		if record.Version == version {
			first = record
		}
		fmt.Sscanf(args[2], "%d", &version)
		if record.Version == version {
			second = record
		}
	}
	if first == nil || second == nil {
		return fmt.Errorf("prompt versions not found")
	}
	changes := []map[string]string{}
	sections := map[string]model.Section{}
	for _, section := range first.Sections {
		sections[section.Name] = section
	}
	for _, section := range second.Sections {
		old, ok := sections[section.Name]
		if !ok {
			changes = append(changes, map[string]string{"section": section.Name, "type": "added"})
		} else if old.Body != section.Body {
			changes = append(changes, map[string]string{"section": section.Name, "type": "modified"})
		}
		delete(sections, section.Name)
	}
	for name := range sections {
		changes = append(changes, map[string]string{"section": name, "type": "removed"})
	}
	respond(ctx, map[string]any{"success": true, "command": "diff", "name": args[0], "from": args[1], "to": args[2], "changes": changes}, fmt.Sprintf("%d change(s)", len(changes)))
	return nil
}

// sync deliberately performs the same scoped Git operation as the legacy CLI:
// only project records are staged, never unrelated worktree changes.
func sync(ctx *context, args []string) error {
	if _, err := paths.BeaconDir(ctx.cwd); err != nil {
		return err
	}
	git := func(arguments ...string) ([]byte, error) {
		command := exec.Command("git", arguments...)
		command.Dir = ctx.cwd
		return command.CombinedOutput()
	}
	status, err := git("status", "--porcelain", ".luuna/beacon/")
	if err != nil {
		return fmt.Errorf("git status: %s", strings.TrimSpace(string(status)))
	}
	lines := []string{}
	if trimmed := strings.TrimSpace(string(status)); trimmed != "" {
		for _, line := range strings.Split(trimmed, "\n") {
			if len(line) >= 4 {
				lines = append(lines, strings.TrimSpace(line[3:]))
			}
		}
	}
	if has(args, "--status") {
		respond(ctx, map[string]any{"success": true, "command": "sync", "uncommitted": len(lines) > 0, "files": lines}, fmt.Sprintf("%d uncommitted file(s)", len(lines)))
		return nil
	}
	if len(lines) == 0 {
		respond(ctx, map[string]any{"success": true, "command": "sync", "committed": false, "message": "Nothing to commit"}, "Nothing to commit in .luuna/beacon/")
		return nil
	}
	if output, err := git("add", ".luuna/beacon/"); err != nil {
		return fmt.Errorf("git add: %s", strings.TrimSpace(string(output)))
	}
	message := "luuna-beacon: sync " + time.Now().UTC().Format("2006-01-02")
	if output, err := git("commit", "-m", message); err != nil {
		return fmt.Errorf("git commit: %s", strings.TrimSpace(string(output)))
	}
	respond(ctx, map[string]any{"success": true, "command": "sync", "committed": true, "files": lines, "message": message}, "Committed "+fmt.Sprint(len(lines))+" file(s): "+message)
	return nil
}

func loadConfig(ctx *context) (model.Config, error) {
	path, err := paths.Config(ctx.cwd)
	if err != nil {
		return model.Config{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return model.Config{}, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return model.Config{}, err
	}
	config := model.Config{Project: "luuna-beacon", Version: "1"}
	if project, ok := raw["project"].(string); ok && project != "" {
		config.Project = project
	}
	if version, ok := raw["version"].(string); ok && version != "" {
		config.Version = version
	}
	if targets, ok := raw["targets"].(map[string]any); ok {
		config.Targets = map[string]model.EmitTarget{}
		for name, rawTarget := range targets {
			fields, ok := rawTarget.(map[string]any)
			if !ok {
				continue
			}
			target := model.EmitTarget{Dir: name}
			if dir, ok := fields["dir"].(string); ok {
				target.Dir = dir
			}
			if def, ok := fields["default"].(bool); ok {
				target.Default = def
			}
			if def, ok := fields["default"].(string); ok {
				target.Default = def == "true"
			}
			if values, ok := fields["tags"].([]any); ok {
				for _, value := range values {
					if tag, ok := value.(string); ok {
						target.Tags = append(target.Tags, tag)
					}
				}
			}
			config.Targets[name] = target
		}
		return config, nil
	}
	// Legacy emitDir/emitDirByTag maps are migrated in memory.  In particular,
	// routing must not disappear when a repository only supplied emitDirByTag.
	legacyDir, _ := raw["emitDir"].(string)
	byTag, hasTags := raw["emitDirByTag"].(map[string]any)
	if legacyDir == "" && !hasTags {
		return config, nil
	}
	if legacyDir == "" {
		legacyDir = "agents"
	}
	config.Targets = map[string]model.EmitTarget{"default": {Dir: legacyDir, Default: true}}
	dirs := map[string][]string{}
	for tag, rawDir := range byTag {
		if dir, ok := rawDir.(string); ok && dir != "" {
			dirs[dir] = append(dirs[dir], tag)
		}
	}
	for dir, tags := range dirs {
		if dir == legacyDir {
			target := config.Targets["default"]
			target.Tags = tags
			config.Targets["default"] = target
			continue
		}
		name := regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(dir, "-")
		name = strings.Trim(name, "-")
		if name == "" {
			name = "target"
		}
		for candidate := name; ; candidate += "-2" {
			if _, exists := config.Targets[candidate]; !exists {
				config.Targets[candidate] = model.EmitTarget{Dir: dir, Tags: tags}
				break
			}
		}
	}
	return config, nil
}
func getPath(root map[string]any, dotted string) (any, bool) {
	var current any = root
	for _, part := range strings.Split(dotted, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
func setPath(root map[string]any, dotted string, replacement any) error {
	parts := strings.Split(dotted, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("config path must be non-empty")
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part]
		if !ok {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot set: %q is not an object", part)
		}
		current = child
	}
	current[parts[len(parts)-1]] = replacement
	return nil
}
func unsetPath(root map[string]any, dotted string) bool {
	parts := strings.Split(dotted, ".")
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	_, ok := current[parts[len(parts)-1]]
	if ok {
		delete(current, parts[len(parts)-1])
	}
	return ok
}
func saveRawConfig(path string, raw map[string]any) error {
	lock, err := store.Acquire(path)
	if err != nil {
		return err
	}
	defer lock.Release()
	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, encoded)
}
func validateRawConfig(raw map[string]any) error {
	project, projectOK := raw["project"].(string)
	version, versionOK := raw["version"].(string)
	if !projectOK || strings.TrimSpace(project) == "" {
		return fmt.Errorf("config validation failed: project must be a non-empty string")
	}
	if !versionOK || strings.TrimSpace(version) == "" {
		return fmt.Errorf("config validation failed: version must be a non-empty string")
	}
	for key := range raw {
		if key != "project" && key != "version" && key != "targets" {
			return fmt.Errorf("config validation failed: unknown property %q", key)
		}
	}
	if targets, exists := raw["targets"]; exists {
		entries, ok := targets.(map[string]any)
		if !ok {
			return fmt.Errorf("config validation failed: targets must be an object")
		}
		for name, value := range entries {
			target, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("config validation failed: targets.%s must be an object", name)
			}
			dir, ok := target["dir"].(string)
			if !ok || strings.TrimSpace(dir) == "" {
				return fmt.Errorf("config validation failed: targets.%s.dir must be a non-empty string", name)
			}
			if value, ok := target["default"]; ok {
				if _, boolOK := value.(bool); !boolOK {
					return fmt.Errorf("config validation failed: targets.%s.default must be boolean", name)
				}
			}
			if value, ok := target["tags"]; ok {
				values, listOK := value.([]any)
				if !listOK {
					return fmt.Errorf("config validation failed: targets.%s.tags must be an array", name)
				}
				for _, tag := range values {
					if _, stringOK := tag.(string); !stringOK {
						return fmt.Errorf("config validation failed: targets.%s.tags must contain strings", name)
					}
				}
			}
		}
	}
	return nil
}
func emitDir(prompt model.Prompt, config model.Config) string {
	if prompt.EmitDir != "" {
		return prompt.EmitDir
	}
	for _, target := range config.Targets {
		for _, tag := range prompt.Tags {
			if contains(target.Tags, tag) {
				return target.Dir
			}
		}
	}
	for _, target := range config.Targets {
		if target.Default {
			return target.Dir
		}
	}
	return "agents"
}
func renderMarkdown(prompt model.Prompt) string {
	bodies := make([]string, 0, len(prompt.Sections))
	if len(prompt.Sections) == 1 {
		bodies = append(bodies, prompt.Sections[0].Body)
	} else {
		for _, section := range prompt.Sections {
			bodies = append(bodies, "## "+section.Name+"\n\n"+section.Body)
		}
	}
	body := strings.Join(bodies, "\n\n") + "\n"
	if strings.HasSuffix(prompt.EmitAs, ".ts") {
		exportName := strings.ToUpper(strings.ReplaceAll(prompt.Name, "-", "_"))
		body = strings.ReplaceAll(body, "\\", "\\\\")
		body = strings.ReplaceAll(body, "`", "\\`")
		body = strings.ReplaceAll(body, "${", "\\${")
		description := ""
		if prompt.Description != "" {
			description = "// " + prompt.Description + "\n"
		}
		return "// Auto-generated by Luuna Beacon — do not edit manually\n// Prompt: " + prompt.Name + "\n" + description + "\nexport const " + exportName + " = `" + body + "`;\n"
	}
	frontmatter := map[string]any{"name": prompt.Name}
	if prompt.Description != "" {
		frontmatter["description"] = prompt.Description
	}
	for key, value := range prompt.Frontmatter {
		frontmatter[key] = value
	}
	if len(frontmatter) > 1 || prompt.Name != "" {
		encoded, err := yaml.Marshal(frontmatter)
		if err == nil {
			return "---\n" + string(encoded) + "---\n" + body
		}
	}
	return body
}
func completions(ctx *context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing shell argument")
	}
	if !contains([]string{"bash", "zsh", "fish"}, args[0]) {
		return fmt.Errorf("unknown shell: %s", args[0])
	}
	if ctx.quiet {
		return nil
	}
	commands := "init create update archive list show render emit stats tree pin unpin config schema skill agent bundle prime onboard doctor validate import diff sync upgrade history completions"
	switch args[0] {
	case "bash":
		fmt.Fprintf(ctx.out, "# bash completion for luuna-beacon\n_luuna_beacon() {\n  local cur=\"${COMP_WORDS[COMP_CWORD]}\"\n  COMPREPLY=( $(compgen -W '%s' -- \"$cur\") )\n}\ncomplete -F _luuna_beacon luuna-beacon lbc lb\n", commands)
	case "zsh":
		fmt.Fprintf(ctx.out, "#compdef luuna-beacon lbc lb\n_luuna_beacon() {\n  local -a commands\n  commands=(%s)\n  _describe 'command' commands\n}\n_luuna_beacon \"$@\"\n", strings.ReplaceAll(commands, " ", " "))
	case "fish":
		for _, command := range strings.Fields(commands) {
			fmt.Fprintf(ctx.out, "complete -c luuna-beacon -c lbc -c lb -f -a %s\n", command)
		}
	}
	return nil
}

func prompts(ctx *context) ([]model.Prompt, error) {
	path, err := paths.Prompts(ctx.cwd)
	if err != nil {
		return nil, err
	}
	records, err := store.ReadJSONL[model.Prompt](path)
	if err != nil {
		return nil, err
	}
	return current(records), nil
}
func promptsMust(ctx *context) []model.Prompt { records, _ := prompts(ctx); return records }
func current(records []model.Prompt) []model.Prompt {
	byID := map[string]model.Prompt{}
	for _, prompt := range records {
		if existing, ok := byID[prompt.ID]; !ok || prompt.Version > existing.Version {
			byID[prompt.ID] = prompt
		}
	}
	// A renamed prompt leaves its old identity in the log. Normal command
	// lookup must expose only the latest current record for each visible name.
	byName := map[string]model.Prompt{}
	for _, prompt := range byID {
		existing, ok := byName[prompt.Name]
		if !ok || prompt.Version > existing.Version || (prompt.Version == existing.Version && prompt.UpdatedAt > existing.UpdatedAt) {
			byName[prompt.Name] = prompt
		}
	}
	result := make([]model.Prompt, 0, len(byName))
	for _, prompt := range byName {
		result = append(result, prompt)
	}
	return result
}
func find(records []model.Prompt, reference string) (model.Prompt, error) {
	name, rawVersion, wantsVersion := strings.Cut(reference, "@")
	var selected *model.Prompt
	for index := range records {
		prompt := &records[index]
		if prompt.Name != name {
			continue
		}
		if wantsVersion {
			var wanted int
			if _, err := fmt.Sscanf(rawVersion, "%d", &wanted); err != nil || prompt.Version != wanted {
				continue
			}
		}
		if selected == nil || prompt.Version > selected.Version || (prompt.Version == selected.Version && prompt.UpdatedAt > selected.UpdatedAt) {
			selected = prompt
		}
	}
	if selected != nil {
		return *selected, nil
	}
	return model.Prompt{}, fmt.Errorf("prompt %q not found", reference)
}
func respond(ctx *context, object any, human string) {
	if ctx.json {
		writeJSON(ctx.out, object)
	} else if !ctx.quiet {
		fmt.Fprintln(ctx.out, human)
	}
}
func writeJSON(out io.Writer, object any) {
	encoded, _ := json.MarshalIndent(object, "", "  ")
	fmt.Fprintln(out, string(encoded))
}
func takeFlag(args []string, flag string) ([]string, bool) {
	result := []string{}
	found := false
	for _, arg := range args {
		if arg == flag {
			found = true
		} else {
			result = append(result, arg)
		}
	}
	return result, found
}
func has(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}
func option(args []string, key string) (string, bool) {
	for index, arg := range args {
		if arg == key && index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
			return args[index+1], true
		}
	}
	return "", false
}
func value(args []string, key string) string { result, _ := option(args, key); return result }
func options(args []string, key string) []string {
	values := []string{}
	for index, arg := range args {
		if arg == key && index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
			values = append(values, args[index+1])
		}
	}
	return values
}

// Parse the CLI's repeatable section grammar: both `--section Name=body` and
// `--section Name --body body` are public forms.  The latter is associated with
// the immediately preceding section, matching Commander-based legacy parsing.
func parseSections(args []string) []model.Section {
	return parseNamedSections(args, "--section")
}
func parseNamedSections(args []string, key string) []model.Section {
	sections := []model.Section{}
	for index := 0; index < len(args); index++ {
		if args[index] != key || index+1 >= len(args) {
			continue
		}
		raw := args[index+1]
		index++
		name, body, inline := strings.Cut(raw, "=")
		if !inline && index+2 < len(args) && args[index+1] == "--body" {
			body = args[index+2]
			index += 2
		}
		if name != "" {
			sections = append(sections, model.Section{Name: name, Body: body})
		}
	}
	return sections
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func without(values []string, unwanted string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != unwanted {
			result = append(result, value)
		}
	}
	return result
}
func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func help(out io.Writer) {
	fmt.Fprintln(out, "Luuna Beacon v0.2.6 — Prompt management & composition\n\nUsage: luuna-beacon <command> [options]")
}
func commandHelp(out io.Writer, args []string) {
	fmt.Fprintf(out, "Usage: luuna-beacon %s [options]\n", strings.Join(args, " "))
}
