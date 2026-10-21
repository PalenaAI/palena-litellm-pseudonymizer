// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/presidio"
)

// maxInflection is the longest ending AllowListed accepts after a term:
// Hungarian "Linuxra", "Dockerrel", Finnish "Linuxille", Polish "Linuksa"
// is out of reach (the stem itself changes) and needs its own entry.
const maxInflection = 5

// AllowListed reports whether span is an allow-listed term (set holds
// lower-cased terms), either exactly or followed by a short inflection
// ending of letters, optionally after a hyphen or apostrophe ("Linuxra",
// "OOMKilled-del", "Docker's"). Terms shorter than three characters only
// match exactly. A span that merely contains a term among other words
// ("Siri Johnson") is NOT allow-listed: that may be a person.
func AllowListed(set map[string]struct{}, span string) bool {
	s := strings.ToLower(strings.TrimSpace(span))
	if s == "" {
		return false
	}
	if _, ok := set[s]; ok {
		return true
	}
	for term := range set {
		if utf8.RuneCountInString(term) < 3 || !strings.HasPrefix(s, term) {
			continue
		}
		if isInflection(s[len(term):]) {
			return true
		}
	}
	return false
}

func isInflection(rest string) bool {
	rest = strings.TrimLeft(rest, "-'’")
	n := 0
	for _, r := range rest {
		if !unicode.IsLetter(r) {
			return false
		}
		n++
	}
	return n >= 1 && n <= maxInflection
}

// BuiltinAllowList is the default set of product, platform and tool names a
// span model tends to tag as people in chat about software. Only names that
// are not also common given names or surnames are listed: "Alexa", "Siri",
// "Claude", "Jenkins" or "Kafka" are real people's names too, so allow-listing
// them by default would leak those people — operators add them when their
// traffic is about the products. Disable with ALLOW_LIST_BUILTIN=false.
var BuiltinAllowList = []string{
	// Assistants and AI products
	"ChatGPT", "Copilot", "Gemini", "OpenAI", "Anthropic", "Mistral AI", "Perplexity",
	// Password managers and security tools
	"Bitwarden", "Vaultwarden", "KeePass", "KeePassXC", "1Password", "LastPass", "Dashlane",
	"Proton Pass", "ProtonMail", "Proton Mail", "Passbolt", "WireGuard", "OpenVPN",
	// Operating systems
	"Linux", "Ubuntu", "Debian", "Fedora", "Arch Linux", "macOS", "iOS", "iPadOS", "Android", "Windows",
	// Containers, cloud and infrastructure
	"Docker", "Podman", "Kubernetes", "kubectl", "OpenShift", "Terraform", "Ansible", "Pulumi",
	"Heroku", "Vercel", "Netlify", "Cloudflare", "Hetzner", "DigitalOcean", "Fly.io", "AWS", "Azure",
	"Google Cloud", "OOMKilled", "Nginx", "Traefik", "Grafana", "Prometheus", "Kibana", "Elasticsearch",
	// Developer tools and data stores
	"GitHub", "GitLab", "Bitbucket", "Jira", "Confluence", "VS Code", "Visual Studio Code", "IntelliJ",
	"PostgreSQL", "Postgres", "MySQL", "MariaDB", "MongoDB", "Redis", "SQLite", "ClickHouse", "Spring Boot",
	"Node.js", "TypeScript", "JavaScript", "Python", "Golang",
	// Office and collaboration
	"Excel", "PowerPoint", "Outlook", "OneDrive", "SharePoint", "Teams", "Slack", "Notion", "Figma", "Zoom",
}

// filterImplausible drops PERSON detections that cannot be a name: no
// letters, more than five words, four words or more in lower case only —
// "hi which passwd mgr" was pseudonymized as a person in production — or a
// single lower-case word of at most three letters ("mgr", "brb", "nvm", which
// the span model tags in chat-speak). Real lower-case names ("thomas huber",
// "simone de luca", "peter") pass; a bare lower-case three-letter first name
// ("eva") is the accepted cost.
func filterImplausible(detections []presidio.Detection) []presidio.Detection {
	out := detections[:0:0]
	for _, d := range detections {
		if d.EntityType == "PERSON" && !plausiblePerson(d.Text) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func plausiblePerson(s string) bool {
	words := strings.Fields(s)
	if len(words) == 0 || len(words) > 5 {
		return false
	}
	letters, upper := false, false
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters = true
			if !unicode.IsLower(r) {
				upper = true
			}
		}
	}
	if !letters {
		return false
	}
	if upper {
		return true
	}
	return len(words) < 4 && (len(words) > 1 || utf8.RuneCountInString(words[0]) > 3)
}
