package releasebigqueryloader

import (
	"strings"

	"github.com/anaskhan96/soup"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

// Changelog scrapes the release controller's generated HTML for a release,
// and converts it into a more structured form.  The release controller
// is currently only capable of delivering this data to us in rendered HTML,
// as it passes through several asynchronous systems before the release
// controller even sees it.
type Changelog struct {
	releaseTag string
	root       soup.Root
}

func NewChangelog(releaseTag, html string) *Changelog {
	return &Changelog{
		releaseTag: releaseTag,
		root:       soup.HTMLParse(html),
	}
}

func (c *Changelog) PreviousReleaseTag() string {
	headings := c.root.FindAll("h2")
	for _, heading := range headings {
		if strings.Contains(heading.Text(), "Changes from") {
			_, previousTag, err := extractAnchor(heading.Find("a"))
			if err != nil {
				continue
			}
			return previousTag
		}
	}

	return ""
}

func (c *Changelog) CoreOSVersion() (currentURL, currentVersion, previousURL, previousVersion, diffURL string) {
	component := c.extractComponent("CoreOS")
	if strings.Contains(component.Text(), "upgraded from") {
		anchors := component.FindAll("a")
		if len(anchors) == 3 {
			currentURL, currentVersion, _ = extractAnchor(anchors[0])
			previousURL, previousVersion, _ = extractAnchor(anchors[1])
			diffURL, _, _ = extractAnchor(anchors[2])
		}
	} else {
		currentURL, currentVersion, _ = extractAnchor(component.Find("a"))
	}

	return
}

func (c *Changelog) KubernetesVersion() string {
	component := c.extractComponent("Kubernetes")
	if component == nil {
		return ""
	}
	parts := strings.Split(component.Text(), " ")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return ""
}

func (c *Changelog) Repositories() []jobrunaggregatorapi.ReleaseRepositoryRow {
	sections := c.root.FindAll("h3")
	if len(sections) == 0 {
		return nil
	}

	rows := make([]jobrunaggregatorapi.ReleaseRepositoryRow, 0)
	for _, section := range sections {
		head, imageName, err := extractAnchor(section.Find("a"))
		if err != nil {
			continue
		}
		row := jobrunaggregatorapi.ReleaseRepositoryRow{
			Name:       imageName,
			ReleaseTag: c.releaseTag,
			Head:       head,
		}
		ul := section.FindNextElementSibling()
		if ul.Error != nil {
			continue
		}
		items := ul.FindAll("li")
		if len(items) == 0 {
			continue
		}
		for _, item := range items {
			url, text, err := extractAnchor(item.Find("a"))
			if err != nil {
				continue
			}
			if strings.Contains(text, "Full changelog") {
				row.FullChangelog = url
			}
		}
		rows = append(rows, row)
	}

	return rows
}

func (c *Changelog) PullRequests() []jobrunaggregatorapi.ReleasePullRequestRow {
	sections := c.root.FindAll("h3")
	if len(sections) == 0 {
		return nil
	}

	rows := make([]jobrunaggregatorapi.ReleasePullRequestRow, 0)
	for _, section := range sections {
		_, imageName, err := extractAnchor(section.Find("a"))
		if err != nil {
			continue
		}
		ul := section.FindNextElementSibling()
		if ul.Error != nil {
			continue
		}
		items := ul.FindAll("li")
		if len(items) == 0 {
			continue
		}
		for _, item := range items {
			if item.Text() != "" {
				row := jobrunaggregatorapi.ReleasePullRequestRow{
					Name:       imageName,
					ReleaseTag: c.releaseTag,
				}
				row.Description = strings.Trim(strings.TrimPrefix(item.Text(), ": "), " ")
				anchors := item.FindAll("a")
				for _, anchor := range anchors {
					url, text, err := extractAnchor(anchor)
					if err != nil {
						continue
					}
					if strings.Contains(url, "github.com") {
						row.URL = url
						row.ID = strings.ReplaceAll(text, "#", "")
					}
					if strings.Contains(url, "bugzilla.redhat.com") {
						row.BugURL = url
					}
				}
				rows = append(rows, row)
			}
		}
	}

	return rows
}

func extractAnchor(anchor soup.Root) (href, text string, err error) {
	if anchor.Error != nil {
		err = anchor.Error
		return
	}
	attrs := anchor.Attrs()
	if link, ok := attrs["href"]; ok {
		href = link
	}
	text = anchor.Text()
	return
}

func (c *Changelog) extractComponent(name string) (component *soup.Root) {
	sections := c.root.FindAll("h3")
	if len(sections) == 0 {
		return nil
	}
	for _, section := range sections {
		if section.Text() == "Components" {
			list := section.FindNextElementSibling()
			if list.Error != nil {
				return nil
			}
			components := list.FindAll("li")
			for _, component := range components {
				if strings.Contains(component.Text(), name) {
					return &component

				}
			}
		}
	}

	return nil
}
