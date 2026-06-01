package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// wantJSON reports whether the user asked for raw JSON output (-o json / --json).
func wantJSON(fs map[string]string) bool {
	if _, ok := fs["json"]; ok {
		return true
	}
	return fs["o"] == "json" || fs["output"] == "json"
}

// table prints aligned columns to stdout.
func table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	_ = w.Flush()
}

type cliArtifact struct {
	Kind     string         `json:"kind"`
	Metadata cliMeta        `json:"metadata"`
	Spec     map[string]any `json:"spec"`
}
type cliMeta struct {
	Name       string `json:"name"`
	Tag        string `json:"tag"`
	Visibility string `json:"visibility"`
	Digest     string `json:"digest"`
}

func (a cliArtifact) title() string {
	if t, ok := a.Spec["title"].(string); ok && t != "" {
		return t
	}
	return a.Metadata.Name
}

func shortHash(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printArtifacts renders a list/search result as a table (or raw JSON).
func printArtifacts(raw []byte, asJSON bool) error {
	if asJSON {
		fmt.Println(prettyJSON(raw))
		return nil
	}
	var arts []cliArtifact
	if err := json.Unmarshal(raw, &arts); err != nil {
		fmt.Println(prettyJSON(raw)) // not an array — show as-is
		return nil
	}
	if len(arts) == 0 {
		fmt.Println("no artifacts")
		return nil
	}
	rows := make([][]string, 0, len(arts))
	for _, a := range arts {
		rows = append(rows, []string{a.Kind, a.Metadata.Name, dash(a.Metadata.Tag), dash(a.Metadata.Visibility), shortHash(a.Metadata.Digest), truncate(a.title(), 48)})
	}
	table([]string{"KIND", "NAME", "VERSION", "VISIBILITY", "DIGEST", "TITLE"}, rows)
	return nil
}

// printRevisions renders the audit timeline as a table (or raw JSON).
func printRevisions(raw []byte, asJSON bool) error {
	if asJSON {
		fmt.Println(prettyJSON(raw))
		return nil
	}
	var revs []struct {
		Tag       string `json:"tag"`
		Revision  int64  `json:"revision"`
		Digest    string `json:"digest"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(raw, &revs); err != nil {
		fmt.Println(prettyJSON(raw))
		return nil
	}
	if len(revs) == 0 {
		fmt.Println("no revisions")
		return nil
	}
	rows := make([][]string, 0, len(revs))
	for i, r := range revs {
		marker := ""
		if i == 0 {
			marker = "  (latest)"
		}
		rows = append(rows, []string{r.Tag + marker, fmt.Sprintf("rev %d", r.Revision), shortHash(r.Digest), tsShort(r.CreatedAt)})
	}
	table([]string{"VERSION", "REVISION", "DIGEST", "CREATED"}, rows)
	return nil
}

// printVersions renders the tag list, marking the newest as latest.
func printVersions(raw []byte, asJSON bool) error {
	if asJSON {
		fmt.Println(prettyJSON(raw))
		return nil
	}
	var v struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Tags) == 0 {
		fmt.Println(prettyJSON(raw))
		return nil
	}
	rows := make([][]string, 0, len(v.Tags))
	for i, t := range v.Tags {
		tag := t
		if i == 0 {
			tag += "  (latest)"
		}
		rows = append(rows, []string{tag})
	}
	table([]string{"VERSION"}, rows)
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func tsShort(s string) string {
	if len(s) >= 19 {
		return strings.Replace(s[:19], "T", " ", 1)
	}
	return s
}
