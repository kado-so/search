// Package kado_cli_non_search exposes the non-Search Kado CLI skill.
package kado_cli_non_search

import (
	"bufio"
	"bytes"
	"embed"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed SKILL.md agents/*
var files embed.FS

const MinimumCLIVersion = "0.1.12"

func Bundle() (map[string][]byte, error) {
	output := make(map[string][]byte)
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		value, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		output[path] = value
		return nil
	})
	return output, err
}

func Version() string {
	value, err := files.ReadFile("SKILL.md")
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(value))
	if !scanner.Scan() || scanner.Text() != "---" {
		return ""
	}
	var frontmatter bytes.Buffer
	for scanner.Scan() {
		if scanner.Text() == "---" {
			var document struct {
				Metadata struct {
					Version string `yaml:"version"`
				} `yaml:"metadata"`
			}
			if yaml.Unmarshal(frontmatter.Bytes(), &document) != nil {
				return ""
			}
			return strings.TrimSpace(document.Metadata.Version)
		}
		frontmatter.WriteString(scanner.Text())
		frontmatter.WriteByte('\n')
	}
	return ""
}
