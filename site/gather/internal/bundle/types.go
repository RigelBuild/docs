// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

type ConfigMode string

const (
	ConfigRequired ConfigMode = "required"
	ConfigDefault  ConfigMode = "default"
)

type Source struct {
	Repo   string     `json:"repo"`
	Mount  string     `json:"mount"`
	Config ConfigMode `json:"config"`
}

type FetchedSource struct {
	Source
	Root string `json:"root"`
	SHA  string `json:"sha"`
}

type SectionRule struct {
	ID       string   `toml:"id"`
	Label    string   `toml:"label"`
	Route    string   `toml:"route"`
	Prefix   *string  `toml:"prefix"`
	Files    []string `toml:"files"`
	Pagefind *bool    `toml:"pagefind"`
}

type PackageRules struct {
	Route       string   `toml:"route"`
	Allow       []string `toml:"allow"`
	ReadmeDepth int      `toml:"readme_depth"`
	Nested      []string `toml:"nested"`
}

type PublishConfig struct {
	Exclude  []string      `toml:"exclude"`
	Sections []SectionRule `toml:"sections"`
	Packages *PackageRules `toml:"packages"`
}

type Page struct {
	Repo       string
	SourcePath string
	Route      string
	Section    string
	Pagefind   bool
}

type Manifest struct {
	DocsRepo struct {
		Repo string `json:"repo"`
		SHA  string `json:"sha"`
	} `json:"docsRepo"`
	Sources []ManifestSource `json:"sources"`
}

type ManifestSource struct {
	Repo  string `json:"repo"`
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
	Pages int    `json:"pages"`
}
