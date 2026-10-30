// Command releasenotes prints the JSON body for a GitHub release: the tag's
// section of CHANGELOG.md and how to pull the image. It fails when the
// changelog has no section for the tag, so a release cannot go out
// undocumented.
//
//	go run ./tools/releasenotes v0.1.0 ghcr.io/chrthal/accountant
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: releasenotes <tag> <image>")
		os.Exit(2)
	}
	tag, image := os.Args[1], os.Args[2]
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	notes, err := Section(string(changelog), strings.TrimPrefix(tag, "v"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	version := strings.TrimPrefix(tag, "v")
	body := notes + "\n\n### Container image\n\n    docker pull " + image + ":" + version +
		"\n\nlinux/amd64. Set BOOKKEEPER_DSN to a postgres:// URL, or mount a volume at /data to use the bundled SQLite default."
	out, _ := json.Marshal(map[string]string{"tag_name": tag, "name": tag, "body": body})
	fmt.Println(string(out))
}

// Section returns the body of the "## [version]" section, without its
// heading.
func Section(changelog, version string) (string, error) {
	heading := "## [" + version + "]"
	var (
		out     []string
		inside  bool
		matched bool
	)
	for _, line := range strings.Split(changelog, "\n") {
		if strings.HasPrefix(line, "## ") {
			inside = strings.HasPrefix(line, heading)
			matched = matched || inside
			continue
		}
		// Link definitions at the end belong to no section.
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]: ") {
			inside = false
		}
		if inside {
			out = append(out, line)
		}
	}
	notes := strings.TrimSpace(strings.Join(out, "\n"))
	if !matched || notes == "" {
		return "", fmt.Errorf("CHANGELOG.md has no %q section with content", heading)
	}
	return notes, nil
}
