package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/kado-so/search/internal/payload"
)

// Extend the existing SPDX document with the actual materialized dependency
// graph, native binaries and private runtime, rather than just top-level names.
func addMCPInventory(encoded []byte, b payload.Bundle) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		return nil, err
	}
	packages, ok := doc["packages"].([]any)
	if !ok {
		return nil, payload.ErrInvalid
	}
	relations, ok := doc["relationships"].([]any)
	if !ok {
		return nil, payload.ErrInvalid
	}
	checksum := func(d string) []any { return []any{map[string]any{"algorithm": "SHA256", "checksumValue": d}} }
	add := func(id, name, version, location, license, digest string) {
		p := map[string]any{"SPDXID": id, "name": name, "versionInfo": version, "downloadLocation": location, "filesAnalyzed": false, "licenseConcluded": "NOASSERTION", "licenseDeclared": license}
		if digest != "" {
			p["checksums"] = checksum(digest)
		}
		packages = append(packages, p)
		relations = append(relations, map[string]any{"spdxElementId": "SPDXRef-Kado", "relationshipType": "DEPENDS_ON", "relatedSpdxElement": id})
	}
	add("SPDXRef-MCP", "@kado-so/mcp", b.Manifest.MCP.Version, "https://github.com/kado-so/mcp/tree/"+b.Manifest.MCP.Commit, "Apache-2.0", payload.Digest(b.Files["mcp/component.gen.json"]))
	add("SPDXRef-Node", "node", b.Manifest.MCP.NodeVersion, "https://nodejs.org/download/release/v"+b.Manifest.MCP.NodeVersion+"/", "NOASSERTION", b.Manifest.MCP.NodeArchiveSHA256)
	var files []any
	for i, f := range b.Manifest.Files {
		id := fmt.Sprintf("SPDXRef-PayloadFile-%d", i+1)
		files = append(files, map[string]any{"SPDXID": id, "fileName": "./" + f.Path, "checksums": checksum(f.SHA256), "licenseConcluded": "NOASSERTION", "copyrightText": "NOASSERTION"})
		relations = append(relations, map[string]any{"spdxElementId": "SPDXRef-DOCUMENT", "relationshipType": "DESCRIBES", "relatedSpdxElement": id})
		if !strings.HasPrefix(f.Path, "mcp/app/node_modules/") || !strings.HasSuffix(f.Path, "/package.json") {
			continue
		}
		var p struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			License any    `json:"license"`
		}
		if json.Unmarshal(b.Files[f.Path], &p) != nil || p.Name == "" || p.Version == "" {
			continue
		}
		license, ok := p.License.(string)
		if !ok || license == "" {
			license = "NOASSERTION"
		}
		add(fmt.Sprintf("SPDXRef-NPM-%d", i+1), p.Name, p.Version, "https://registry.npmjs.org/"+url.PathEscape(p.Name), license, f.SHA256)
	}
	doc["packages"] = packages
	doc["relationships"] = relations
	doc["files"] = files
	value, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(value, '\n'), nil
}
