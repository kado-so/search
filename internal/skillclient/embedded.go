package skillclient

import (
	general "github.com/kado-so/search/skills/kado-cli-non-search"
	search "github.com/kado-so/search/skills/kado-search"
)

const EmbeddedCatalogRevision uint64 = 2

type EmbeddedRelease struct {
	Metadata Metadata
	Files    map[string][]byte
}

func EmbeddedCatalog() (Catalog, map[string]EmbeddedRelease, error) {
	generalFiles, err := general.Bundle()
	if err != nil {
		return Catalog{}, nil, err
	}
	searchFiles, err := search.Bundle()
	if err != nil {
		return Catalog{}, nil, err
	}
	releases := map[string]EmbeddedRelease{
		"kado-cli-non-search:default": {Metadata: embeddedMetadata("kado-cli-non-search", general.Version(), general.MinimumCLIVersion), Files: generalFiles},
		"kado-search:default":         {Metadata: embeddedMetadata("kado-search", search.Version(), search.MinimumCLIVersion), Files: searchFiles},
	}
	catalog := Catalog{SchemaVersion: CatalogSchemaVersion, Revision: EmbeddedCatalogRevision, Skills: []CatalogSkill{
		{Name: "kado-cli-non-search", State: "active", Variants: []CatalogVariant{{ID: "default", Agents: []string{"*"}}}},
		{Name: "kado-search", State: "active", Variants: []CatalogVariant{{ID: "default", Agents: []string{"*"}}}},
		{Name: "kado", State: "retired"},
	}}
	return catalog, releases, nil
}

func embeddedMetadata(name, version, minimum string) Metadata {
	return Metadata{SchemaVersion: SchemaVersion, Name: name, Variant: "default", Version: version, MinimumCLIVersion: minimum}
}

func embeddedVersion(name string) string {
	_, releases, err := EmbeddedCatalog()
	if err != nil {
		return ""
	}
	for _, release := range releases {
		if release.Metadata.Name == name {
			return release.Metadata.Version
		}
	}
	return ""
}
