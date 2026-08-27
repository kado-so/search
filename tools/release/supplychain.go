package main

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kado-so/search/internal/releaseclient"
)

type binaryModule struct {
	Path    string
	Version string
}

type binaryModuleGraph struct {
	Main         binaryModule
	Dependencies []binaryModule
}

func readBinaryModuleGraph(binary []byte, expectedMain string) (binaryModuleGraph, error) {
	info, err := buildinfo.Read(bytes.NewReader(binary))
	if err != nil || info.Main.Path != expectedMain {
		return binaryModuleGraph{}, errors.New("release binary module inventory is invalid")
	}
	graph := binaryModuleGraph{Main: binaryModule{Path: info.Main.Path, Version: info.Main.Version}}
	seen := make(map[string]bool, len(info.Deps))
	for _, dependency := range info.Deps {
		selected := dependency
		if dependency.Replace != nil {
			selected = dependency.Replace
		}
		key := selected.Path + "@" + selected.Version
		if selected.Path == "" || seen[key] {
			continue
		}
		seen[key] = true
		graph.Dependencies = append(graph.Dependencies, binaryModule{
			Path: selected.Path, Version: moduleVersion(selected.Version),
		})
	}
	sort.Slice(graph.Dependencies, func(left, right int) bool {
		if graph.Dependencies[left].Path != graph.Dependencies[right].Path {
			return graph.Dependencies[left].Path < graph.Dependencies[right].Path
		}
		return graph.Dependencies[left].Version < graph.Dependencies[right].Version
	})
	return graph, nil
}

func moduleVersion(value string) string {
	if value == "" {
		return "(devel)"
	}
	return value
}

func makeSBOM(
	input buildInput,
	target buildTarget,
	name string,
	kadoBinary []byte,
	a2aBinary []byte,
) ([]byte, error) {
	kadoModules, err := readBinaryModuleGraph(kadoBinary, "github.com/kado-so/search")
	if err != nil {
		return nil, err
	}
	a2aModules, err := readBinaryModuleGraph(a2aBinary, a2aModule)
	if err != nil {
		return nil, err
	}
	type externalRef struct {
		Category string `json:"referenceCategory"`
		Type     string `json:"referenceType"`
		Locator  string `json:"referenceLocator"`
	}
	type checksum struct {
		Algorithm string `json:"algorithm"`
		Value     string `json:"checksumValue"`
	}
	type spdxPackage struct {
		Name             string        `json:"name"`
		SPDXID           string        `json:"SPDXID"`
		Version          string        `json:"versionInfo"`
		DownloadLocation string        `json:"downloadLocation"`
		FilesAnalyzed    bool          `json:"filesAnalyzed"`
		LicenseConcluded string        `json:"licenseConcluded"`
		LicenseDeclared  string        `json:"licenseDeclared"`
		ExternalRefs     []externalRef `json:"externalRefs,omitempty"`
		Checksums        []checksum    `json:"checksums,omitempty"`
	}
	type relationship struct {
		Element string `json:"spdxElementId"`
		Type    string `json:"relationshipType"`
		Related string `json:"relatedSpdxElement"`
	}
	packages := []spdxPackage{
		{
			Name: "kado", SPDXID: "SPDXRef-Kado", Version: input.source.Version,
			DownloadLocation: input.source.Repository, FilesAnalyzed: false,
			LicenseConcluded: "NOASSERTION", LicenseDeclared: "MIT",
			Checksums: []checksum{{Algorithm: "SHA256", Value: releaseclient.Digest(kadoBinary)}},
		},
		{
			Name: "a2a-cli", SPDXID: "SPDXRef-A2A-CLI", Version: input.a2a.Lock.Version,
			DownloadLocation: input.a2a.Lock.Repository, FilesAnalyzed: false,
			LicenseConcluded: "NOASSERTION", LicenseDeclared: input.a2a.Lock.License.SPDX,
			Checksums: []checksum{{Algorithm: "SHA256", Value: releaseclient.Digest(a2aBinary)}},
		},
	}
	relationships := []relationship{
		{Element: "SPDXRef-DOCUMENT", Type: "DESCRIBES", Related: "SPDXRef-Kado"},
		{Element: "SPDXRef-DOCUMENT", Type: "DESCRIBES", Related: "SPDXRef-A2A-CLI"},
	}
	dependencyIDs := make(map[string]string)
	addDependencies := func(owner string, modules []binaryModule) {
		for _, dependency := range modules {
			key := dependency.Path + "@" + dependency.Version
			id, exists := dependencyIDs[key]
			if !exists {
				id = fmt.Sprintf("SPDXRef-Dependency-%d", len(dependencyIDs)+1)
				dependencyIDs[key] = id
				packages = append(packages, spdxPackage{
					Name: dependency.Path, SPDXID: id, Version: dependency.Version,
					DownloadLocation: "NOASSERTION", FilesAnalyzed: false,
					LicenseConcluded: "NOASSERTION", LicenseDeclared: "NOASSERTION",
					ExternalRefs: []externalRef{{
						Category: "PACKAGE-MANAGER", Type: "purl",
						Locator: "pkg:golang/" + strings.ReplaceAll(dependency.Path, "/", "%2F") + "@" + dependency.Version,
					}},
				})
			}
			relationships = append(relationships, relationship{
				Element: owner, Type: "DEPENDS_ON", Related: id,
			})
		}
	}
	addDependencies("SPDXRef-Kado", kadoModules.Dependencies)
	addDependencies("SPDXRef-A2A-CLI", a2aModules.Dependencies)
	document := struct {
		SPDXVersion       string `json:"spdxVersion"`
		DataLicense       string `json:"dataLicense"`
		SPDXID            string `json:"SPDXID"`
		Name              string `json:"name"`
		DocumentNamespace string `json:"documentNamespace"`
		Comment           string `json:"comment"`
		CreationInfo      struct {
			Created  string   `json:"created"`
			Creators []string `json:"creators"`
		} `json:"creationInfo"`
		Packages      []spdxPackage  `json:"packages"`
		Relationships []relationship `json:"relationships"`
	}{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name:              name,
		DocumentNamespace: "https://kado.so/spdx/kado/" + input.source.Version + "/" + target.goos + "/" + target.goarch,
		Comment:           target.goos + "/" + target.goarch,
		Packages:          packages, Relationships: relationships,
	}
	document.CreationInfo.Created = input.builtAt.Format("2006-01-02T15:04:05Z")
	document.CreationInfo.Creators = []string{"Tool: kado-release"}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, errors.New("release SBOM could not be encoded")
	}
	return append(encoded, '\n'), nil
}

func makeProvenance(
	input buildInput,
	files map[string]builtFile,
	targets []releaseclient.Target,
) ([]byte, error) {
	type subject struct {
		Name   string            `json:"name"`
		Digest map[string]string `json:"digest"`
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	subjects := make([]subject, 0, len(names)+len(targets))
	for _, name := range names {
		subjects = append(subjects, subject{
			Name: name, Digest: map[string]string{"sha256": files[name].file.SHA256},
		})
	}
	for _, target := range targets {
		sidecarName := "kado-a2a"
		if target.OS == "windows" {
			sidecarName += ".exe"
		}
		subjects = append(subjects, subject{
			Name:   target.Archive.Name + "#" + sidecarName,
			Digest: map[string]string{"sha256": target.Sidecar.SHA256},
		})
	}
	type dependency struct {
		URI    string            `json:"uri"`
		Digest map[string]string `json:"digest"`
	}
	statement := struct {
		Type          string    `json:"_type"`
		Subject       []subject `json:"subject"`
		PredicateType string    `json:"predicateType"`
		Predicate     struct {
			BuildDefinition struct {
				BuildType          string `json:"buildType"`
				ExternalParameters struct {
					Version    string `json:"version"`
					KadoCommit string `json:"kado_commit"`
					A2AVersion string `json:"a2a_version"`
					A2ATag     string `json:"a2a_tag"`
					A2ACommit  string `json:"a2a_commit"`
				} `json:"externalParameters"`
				InternalParameters struct {
					GoToolchain      string `json:"go_toolchain"`
					A2ASourceArchive string `json:"a2a_source_archive_sha256"`
					A2ASourceTree    string `json:"a2a_source_tree_sha256"`
					A2APatchedTree   string `json:"a2a_patched_tree_sha256"`
					A2APatchSet      string `json:"a2a_patch_set_sha256"`
					A2ADisplayName   string `json:"a2a_display_name"`
				} `json:"internalParameters"`
				ResolvedDependencies []dependency `json:"resolvedDependencies"`
			} `json:"buildDefinition"`
			RunDetails struct {
				Builder struct {
					ID string `json:"id"`
				} `json:"builder"`
				Metadata struct {
					InvocationID string `json:"invocationId"`
					StartedOn    string `json:"startedOn"`
					FinishedOn   string `json:"finishedOn"`
				} `json:"metadata"`
				Byproducts []any `json:"byproducts"`
			} `json:"runDetails"`
		} `json:"predicate"`
	}{
		Type: "https://in-toto.io/Statement/v1", Subject: subjects,
		PredicateType: "https://slsa.dev/provenance/v1",
	}
	definition := &statement.Predicate.BuildDefinition
	definition.BuildType = "https://kado.so/build-types/go-cli-release/v2"
	definition.ExternalParameters.Version = input.source.Version
	definition.ExternalParameters.KadoCommit = input.commit
	definition.ExternalParameters.A2AVersion = input.a2a.Lock.Version
	definition.ExternalParameters.A2ATag = a2aTagValue(input.a2a.Lock.Tag)
	definition.ExternalParameters.A2ACommit = input.a2a.Lock.Commit
	definition.InternalParameters.GoToolchain = input.a2a.Lock.GoToolchain
	definition.InternalParameters.A2ASourceArchive = input.a2a.Lock.SourceArchiveSHA256
	definition.InternalParameters.A2ASourceTree = input.a2a.Lock.SourceTreeSHA256
	definition.InternalParameters.A2APatchedTree = input.a2a.Lock.PatchedTreeSHA256
	definition.InternalParameters.A2APatchSet = input.a2a.PatchSetSHA256
	definition.InternalParameters.A2ADisplayName = input.a2a.Lock.DisplayName
	definition.ResolvedDependencies = []dependency{
		{URI: "git+" + input.source.Repository + "@" + input.commit, Digest: map[string]string{"gitCommit": input.commit}},
		{URI: "git+" + input.a2a.Lock.Repository + "@" + input.a2a.Lock.Commit, Digest: map[string]string{
			"gitCommit": input.a2a.Lock.Commit, "sha256": input.a2a.Lock.SourceArchiveSHA256,
		}},
	}
	run := &statement.Predicate.RunDetails
	run.Builder.ID = "https://github.com/kado-so/search/tree/main/tools/release"
	run.Metadata.InvocationID = input.source.Version + "@" + input.commit
	run.Metadata.StartedOn = input.builtAt.Format("2006-01-02T15:04:05Z")
	run.Metadata.FinishedOn = run.Metadata.StartedOn
	run.Byproducts = []any{}
	encoded, err := json.Marshal(statement)
	if err != nil {
		return nil, errors.New("release provenance could not be encoded")
	}
	return append(encoded, '\n'), nil
}
