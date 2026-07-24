package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type module struct {
	Path    string
	Version string
	Sum     string
	Main    bool
	Replace *module
}

func loadModules(input buildInput) ([]module, error) {
	encoded, err := commandOutput(input.root, nil, input.goBinary, "list", "-m", "-json", "all")
	if err != nil {
		return nil, errors.New("Go module inventory could not be generated")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var modules []module
	for {
		var value module
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.New("Go module inventory is invalid")
		}
		modules = append(modules, value)
	}
	if len(modules) == 0 || !modules[0].Main ||
		modules[0].Path != "github.com/kado-so/search" {
		return nil, errors.New("Go module inventory did not identify Kado")
	}
	sort.Slice(modules, func(left, right int) bool {
		if modules[left].Main != modules[right].Main {
			return modules[left].Main
		}
		return modules[left].Path < modules[right].Path
	})
	return modules, nil
}

func makeSBOM(
	input buildInput,
	target buildTarget,
	name string,
	modules []module,
) []byte {
	type externalRef struct {
		Category string `json:"referenceCategory"`
		Type     string `json:"referenceType"`
		Locator  string `json:"referenceLocator"`
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
		Checksums        []struct {
			Algorithm string `json:"algorithm"`
			Value     string `json:"checksumValue"`
		} `json:"checksums,omitempty"`
	}
	type relationship struct {
		Element string `json:"spdxElementId"`
		Type    string `json:"relationshipType"`
		Related string `json:"relatedSpdxElement"`
	}
	packages := make([]spdxPackage, 0, len(modules))
	relationships := []relationship{{
		Element: "SPDXRef-DOCUMENT",
		Type:    "DESCRIBES",
		Related: "SPDXRef-Package-0",
	}}
	for index, dependency := range modules {
		version := dependency.Version
		if dependency.Main {
			version = input.source.Plugin.Version
		}
		if version == "" {
			version = "(devel)"
		}
		id := fmt.Sprintf("SPDXRef-Package-%d", index)
		item := spdxPackage{
			Name:             dependency.Path,
			SPDXID:           id,
			Version:          version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
		}
		if dependency.Main {
			item.Name = "kado"
			item.DownloadLocation = input.source.Plugin.Repository
			item.LicenseDeclared = "MIT"
		} else {
			item.ExternalRefs = []externalRef{{
				Category: "PACKAGE-MANAGER",
				Type:     "purl",
				Locator: "pkg:golang/" +
					strings.ReplaceAll(dependency.Path, "/", "%2F") +
					"@" + version,
			}}
		}
		packages = append(packages, item)
		if index > 0 {
			relationships = append(relationships, relationship{
				Element: "SPDXRef-Package-0",
				Type:    "DEPENDS_ON",
				Related: id,
			})
		}
	}
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
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              name,
		DocumentNamespace: "https://kado.so/spdx/kado/" + input.source.Plugin.Version + "/" + target.goos + "/" + target.goarch,
		Comment:           target.goos + "/" + target.goarch,
		Packages:          packages,
		Relationships:     relationships,
	}
	document.CreationInfo.Created = input.builtAt.Format("2006-01-02T15:04:05Z")
	document.CreationInfo.Creators = []string{"Tool: kado-release"}
	encoded, _ := json.Marshal(document)
	return append(encoded, '\n')
}

func makeProvenance(input buildInput, files map[string]builtFile) []byte {
	type subject struct {
		Name   string            `json:"name"`
		Digest map[string]string `json:"digest"`
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	subjects := make([]subject, 0, len(names))
	for _, name := range names {
		subjects = append(subjects, subject{
			Name:   name,
			Digest: map[string]string{"sha256": files[name].file.SHA256},
		})
	}
	statement := struct {
		Type          string    `json:"_type"`
		Subject       []subject `json:"subject"`
		PredicateType string    `json:"predicateType"`
		Predicate     struct {
			BuildDefinition struct {
				BuildType          string `json:"buildType"`
				ExternalParameters struct {
					Version string `json:"version"`
					Commit  string `json:"commit"`
				} `json:"externalParameters"`
				InternalParameters   map[string]any `json:"internalParameters"`
				ResolvedDependencies []struct {
					URI    string            `json:"uri"`
					Digest map[string]string `json:"digest"`
				} `json:"resolvedDependencies"`
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
		Type:          "https://in-toto.io/Statement/v1",
		Subject:       subjects,
		PredicateType: "https://slsa.dev/provenance/v1",
	}
	statement.Predicate.BuildDefinition.ExternalParameters.Version =
		input.source.Plugin.Version
	statement.Predicate.BuildDefinition.ExternalParameters.Commit = input.commit
	statement.Predicate.BuildDefinition.BuildType =
		"https://kado.so/build-types/go-cli-release/v1"
	statement.Predicate.BuildDefinition.InternalParameters = map[string]any{}
	statement.Predicate.BuildDefinition.ResolvedDependencies = append(
		statement.Predicate.BuildDefinition.ResolvedDependencies,
		struct {
			URI    string            `json:"uri"`
			Digest map[string]string `json:"digest"`
		}{
			URI:    "git+" + input.source.Plugin.Repository + "@" + input.commit,
			Digest: map[string]string{"gitCommit": input.commit},
		},
	)
	statement.Predicate.RunDetails.Builder.ID =
		"https://github.com/kado-so/search/.github/workflows/cli-release.yml"
	statement.Predicate.RunDetails.Metadata.InvocationID =
		input.source.Plugin.Version + "@" + input.commit
	statement.Predicate.RunDetails.Metadata.StartedOn =
		input.builtAt.Format("2006-01-02T15:04:05Z")
	statement.Predicate.RunDetails.Metadata.FinishedOn =
		input.builtAt.Format("2006-01-02T15:04:05Z")
	statement.Predicate.RunDetails.Byproducts = []any{}
	encoded, _ := json.Marshal(statement)
	return append(encoded, '\n')
}
