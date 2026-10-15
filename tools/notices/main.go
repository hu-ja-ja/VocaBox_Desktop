// Command notices writes what the app says about the licences of the works
// it bundles: notices_gen.go, the entries of the settings page, and
// THIRD_PARTY_NOTICES.md, the same list as a document in the repository.
//
// The crates come from cargo-about and the modules from go-licenses, both
// installed with the rest of the toolchain by mise (mise.toml):
//
//	go run ./tools/notices         # write the files
//	go run ./tools/notices -check  # report stale files instead
package main

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// headerMD is the hand-written part of the notices: what the app is
// published under, and what it bundles.
//
//go:embed header.md
var headerMD string

// iconsMD is the Lucide part of the notices: what the icons are, the names in
// it, and the terms they are under. The app shows the same text as the entry
// of its licence list.
//
//go:embed icons.md
var iconsMD string

// iconMarker is the line icons.md leaves for the list of icon names.
const iconMarker = "<!-- icons -->"

// wrapWidth is how wide the wrapped lines of the icon list are.
const wrapWidth = 76

// groups name where a bundled work comes from, as the notices head them.
const (
	lucideIcons = "Lucide icons"
	goModules   = "Go modules"
	rustCrates  = "Rust crates"
	cratesURI   = "https://crates.io/crates/"
	noticesPath = "THIRD_PARTY_NOTICES.md"
	entriesPath = "notices_gen.go"
)

// entry is one bundled work: what it is, the terms it is bundled under, and
// the text of those terms.
type entry struct {
	Name      string
	Version   string
	Group     string
	License   string
	Copyright string
	URL       string
	Text      string
}

func main() {
	check := flag.Bool("check", false, "report stale files instead of writing them")
	flag.Parse()

	file, module, err := mainModule()
	if err != nil {
		fatal(err)
	}
	root := filepath.Dir(file)
	entries, err := collect(root, module)
	if err != nil {
		fatal(err)
	}
	notices, err := renderNotices(root, entries)
	if err != nil {
		fatal(err)
	}
	generated, err := renderEntries(entries)
	if err != nil {
		fatal(err)
	}
	for _, f := range []struct {
		path string
		want []byte
	}{{filepath.Join(root, noticesPath), notices}, {filepath.Join(root, entriesPath), generated}} {
		if *check {
			old, err := os.ReadFile(f.path)
			if err != nil {
				fatal(err)
			}
			if !bytes.Equal(old, f.want) {
				fatal(fmt.Errorf("%s is out of date: run go run ./tools/notices", filepath.Base(f.path)))
			}
			continue
		}
		if err := os.WriteFile(f.path, f.want, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", f.path, len(f.want))
	}
}

// mainModule returns the go.mod of the module the command runs in: the path
// of its file, and its module path, which the notices leave out as it is the
// app itself.
func mainModule() (file, module string, err error) {
	out, err := output("", "go", "env", "GOMOD")
	if err != nil {
		return "", "", err
	}
	file = strings.TrimSpace(out)
	if file == "" || file == os.DevNull {
		return "", "", errors.New("no go.mod: run this from the repository")
	}
	out, err = output(filepath.Dir(file), "go", "list", "-m", "-json")
	if err != nil {
		return "", "", err
	}
	var m struct{ Path string }
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return "", "", err
	}
	return file, m.Path, nil
}

// collect lists every work the app bundles, the icons it takes from Lucide,
// the Go modules it links and the crates the helper bundles, sorted by name.
func collect(root, module string) ([]entry, error) {
	icons, err := iconEntry(root)
	if err != nil {
		return nil, err
	}
	modules, err := goEntries(root, module)
	if err != nil {
		return nil, err
	}
	crates, err := rustEntries(root)
	if err != nil {
		return nil, err
	}
	all := append([]entry{icons}, append(modules, crates...)...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

// iconEntry is the Lucide icons the app takes: the entry of the list, whose
// text the notices show as their own section.
func iconEntry(root string) (entry, error) {
	names, err := iconList(root)
	if err != nil {
		return entry{}, err
	}
	return entry{
		Name:    "Lucide",
		Group:   lucideIcons,
		License: "ISC, MIT",
		URL:     "https://lucide.dev",
		Text:    strings.Replace(iconsMD, iconMarker, names, 1),
	}, nil
}

// goEntries lists the modules the app links: go-licenses names their licences
// and saves their texts.
func goEntries(root, module string) ([]entry, error) {
	modules, err := linkedModules(root)
	if err != nil {
		return nil, err
	}
	reports, err := licenseReports(root, module)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "notices-go")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if _, err := output(root, "go-licenses", "save", "./app", "--force",
		"--save_path", dir, "--ignore", module); err != nil {
		return nil, err
	}
	var entries []entry
	for _, m := range modules {
		texts, err := savedLicense(dir, m.Path)
		if err != nil {
			return nil, err
		}
		report := reports[m.Path]
		entries = append(entries, entry{
			Name:    m.Path,
			Version: m.Version,
			Group:   goModules,
			License: report.license,
			URL:     report.url,
			Text:    clean(strings.Join(texts, "\n\n")),
		})
	}
	return entries, nil
}

// goModule is one module the app links.
type goModule struct {
	Path    string
	Version string
}

// linkedModules names the Go modules whose code ends up in the app, with the
// versions the build uses. Packages with no module, the standard library
// among them, and the app's own module are left out.
func linkedModules(root string) ([]goModule, error) {
	out, err := output(root, "go", "list", "-deps",
		"-f", "{{with .Module}}{{.Path}}|{{.Version}}{{end}}", "./app")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var modules []goModule
	for _, line := range strings.Split(out, "\n") {
		path, version, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || path == "" || version == "" || seen[path] {
			continue
		}
		seen[path] = true
		modules = append(modules, goModule{Path: path, Version: version})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	return modules, nil
}

// licenseReport is what go-licenses says about one module: the licences it
// found, and where their text comes from.
type licenseReport struct {
	license string
	url     string
}

// licenseReports reads go-licenses' report, module path → its licences.
func licenseReports(root, module string) (map[string]licenseReport, error) {
	out, err := output(root, "go-licenses", "report", "./app", "--ignore", module)
	if err != nil {
		return nil, err
	}
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		return nil, err
	}
	reports := map[string][]string{}
	urls := map[string]string{}
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		if !contains(reports[row[0]], row[2]) {
			reports[row[0]] = append(reports[row[0]], row[2])
		}
		if urls[row[0]] == "" {
			urls[row[0]] = row[1]
		}
	}
	out2 := map[string]licenseReport{}
	for path, list := range reports {
		sort.Strings(list)
		out2[path] = licenseReport{license: strings.Join(list, ", "), url: urls[path]}
	}
	return out2, nil
}

// savedLicense reads the licence files go-licenses saved for one module.
func savedLicense(dir, module string) ([]string, error) {
	base := filepath.Join(dir, filepath.FromSlash(module))
	var files []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var texts []string
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		texts = append(texts, clean(string(b)))
	}
	return texts, nil
}

// rustEntries flattens what cargo-about found in the helper's crates: one
// entry per crate, under the licence whose text the app shows for it.
func rustEntries(root string) ([]entry, error) {
	dir, err := os.MkdirTemp("", "notices-rust")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "about.json")
	if _, err := output(root, "cargo", "about", "generate", "--format", "json",
		"-m", filepath.Join("helper", "Cargo.toml"),
		"-c", filepath.Join("tools", "notices", "about.toml"),
		"-o", out); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	var about struct {
		Licenses []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			Used []struct {
				Crate struct {
					Name       string   `json:"name"`
					Version    string   `json:"version"`
					Authors    []string `json:"authors"`
					Repository string   `json:"repository"`
				} `json:"crate"`
			} `json:"used_by"`
		} `json:"licenses"`
	}
	if err := json.Unmarshal(b, &about); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var entries []entry
	for _, license := range about.Licenses {
		for _, used := range license.Used {
			crate := used.Crate
			key := crate.Name + "@" + crate.Version
			if crate.Name == "" || seen[key] {
				continue
			}
			seen[key] = true
			url := crate.Repository
			if url == "" {
				url = cratesURI + crate.Name
			}
			entries = append(entries, entry{
				Name:      crate.Name,
				Version:   crate.Version,
				Group:     rustCrates,
				License:   license.ID,
				Copyright: strings.Join(crate.Authors, ", "),
				URL:       url,
				Text:      clean(license.Text),
			})
		}
	}
	return entries, nil
}

// renderEntries writes the entries the app shows, as Go source. The texts
// stand in a table of their own, one copy each, which the entries point
// into: a licence used by many works is written once.
func renderEntries(entries []entry) ([]byte, error) {
	var texts []string
	at := map[string]int{}
	for _, e := range entries {
		if _, ok := at[e.Text]; !ok {
			at[e.Text] = len(texts)
			texts = append(texts, e.Text)
		}
	}
	var b strings.Builder
	b.WriteString("// Code generated by tools/notices; DO NOT EDIT.\n\npackage vocabox\n\n")
	b.WriteString(`// Entry is one third-party work the app bundles: what it is, and the terms
// it is bundled under.
type Entry struct {
	Name      string
	Version   string
	Group     string
	License   string
	Copyright string
	URL       string
	// TextIndex is where the terms of the work stand in Texts.
	TextIndex int
}

// Text is the terms the work is bundled under.
func (e Entry) Text() string { return Texts[e.TextIndex] }

// Groups of the entries, as the notices head them.
const (
	GroupLucideIcons = "Lucide icons"
	GroupGoModules   = "Go modules"
	GroupRustCrates  = "Rust crates"
)

// Entries lists the works the app and its playback helper bundle, sorted by
// name.
var Entries = []Entry{
`)
	for _, e := range entries {
		fmt.Fprintf(&b, "\t{Name: %q, Version: %q, Group: %s, License: %q, Copyright: %q, URL: %q, TextIndex: %d},\n",
			e.Name, e.Version, groupConst(e.Group), e.License, e.Copyright, e.URL, at[e.Text])
	}
	b.WriteString("}\n\n// Texts holds the terms, one copy each.\nvar Texts = []string{\n")
	for _, text := range texts {
		fmt.Fprintf(&b, "\t%s,\n", literal(text))
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("formatting the entries: %w", err)
	}
	return src, nil
}

// renderNotices writes the same list as a document, with the header above it
// and one block per licence rather than per work.
func renderNotices(root string, entries []entry) ([]byte, error) {
	var b strings.Builder
	b.WriteString(headerMD)
	for _, group := range []string{lucideIcons, goModules, rustCrates} {
		inGroup := filterGroup(entries, group)
		if len(inGroup) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", group)
		if intro := groupIntro(group); intro != "" {
			fmt.Fprintf(&b, "\n%s\n", intro)
		}
		for _, block := range textBlocks(inGroup) {
			fmt.Fprintf(&b, "\n### %s\n\nUsed by:\n", block.license)
			for _, e := range block.entries {
				fmt.Fprintf(&b, "\n- %s", e.Name)
				if e.Version != "" {
					fmt.Fprintf(&b, " %s", e.Version)
				}
			}
			fmt.Fprintf(&b, "\n\n```\n%s\n```\n", block.text)
		}
	}
	return []byte(b.String()), nil
}

// textBlock is one licence text of a group, and the works sharing it.
type textBlock struct {
	license string
	text    string
	entries []entry
}

// textBlocks groups a group's works by the licence text they carry, so a
// licence used by many works is written once.
func textBlocks(entries []entry) []textBlock {
	var blocks []textBlock
	at := map[string]int{}
	for _, e := range entries {
		key := e.License + "\x00" + e.Text
		if i, ok := at[key]; ok {
			blocks[i].entries = append(blocks[i].entries, e)
			continue
		}
		at[key] = len(blocks)
		blocks = append(blocks, textBlock{license: e.License, text: e.Text, entries: []entry{e}})
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].license != blocks[j].license {
			return blocks[i].license < blocks[j].license
		}
		return blocks[i].entries[0].Name < blocks[j].entries[0].Name
	})
	for i := range blocks {
		sort.Slice(blocks[i].entries, func(a, b int) bool {
			return blocks[i].entries[a].Name < blocks[i].entries[b].Name
		})
	}
	return blocks
}

// groupConst is the generated constant naming one group.
func groupConst(group string) string {
	switch group {
	case lucideIcons:
		return "GroupLucideIcons"
	case goModules:
		return "GroupGoModules"
	}
	return "GroupRustCrates"
}

// groupIntro says what one group of the notices is, where its heading does
// not already.
func groupIntro(group string) string {
	switch group {
	case goModules:
		return "The app links the modules below."
	case rustCrates:
		return "AmsHelper, the playback helper, bundles the crates below."
	}
	return ""
}

func filterGroup(entries []entry, group string) []entry {
	var out []entry
	for _, e := range entries {
		if e.Group == group {
			out = append(out, e)
		}
	}
	return out
}

// iconList names the icons the app takes from Lucide: the ones its source
// embeds, wrapped to keep the file readable.
func iconList(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "app", "icons.go"))
	if err != nil {
		return "", err
	}
	var names []string
	for _, line := range strings.Split(string(b), "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "//go:embed icons/")
		if !ok || !strings.HasSuffix(name, ".svg") {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".svg"))
	}
	if len(names) == 0 {
		return "", errors.New("app/icons.go embeds no icons")
	}
	sort.Strings(names)
	return wrapList(names), nil
}

// wrapList joins names with commas, breaking the line before the name that
// would run past wrapWidth; Markdown reads the breaks as spaces.
func wrapList(names []string) string {
	var b strings.Builder
	line := 0
	for i, name := range names {
		if i > 0 {
			if line+len(", ")+len(name) > wrapWidth {
				b.WriteString(",\n")
				line = 0
			} else {
				b.WriteString(", ")
				line += len(", ")
			}
		}
		b.WriteString(name)
		line += len(name)
	}
	return b.String()
}

// literal renders s as a Go string literal: a raw string, which keeps the
// newlines of a licence text readable in the generated file. A backtick,
// which a raw string cannot hold, is written between raw strings of its own.
func literal(s string) string {
	parts := strings.Split(s, "`")
	for i, part := range parts {
		parts[i] = "`" + part + "`"
	}
	return strings.Join(parts, " + \"`\" + ")
}

// clean is a licence text as the notices carry it: line breaks of the app's
// own, and no trailing blank lines.
func clean(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimRight(text, "\n")
}

// output runs one of the tools in dir and returns what it wrote; a failure
// carries what it said on standard error.
func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	call := strings.Join(append([]string{name}, args...), " ")
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return "", fmt.Errorf("%s: %w\n%s", call, err, msg)
	}
	return "", fmt.Errorf("%s: %w", call, err)
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "notices:", err)
	os.Exit(1)
}
