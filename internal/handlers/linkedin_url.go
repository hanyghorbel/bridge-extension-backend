package handlers

import (
	"fmt"
	"regexp"
	"strings"
)

var linkedInCompanySlugPattern = regexp.MustCompile(`(?i)linkedin\.com/company/([^/?#]+)`)

func extractLinkedInCompanySlug(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty linkedin url")
	}

	withoutQuery := strings.Split(trimmed, "?")[0]
	withoutQuery = strings.TrimSuffix(withoutQuery, "/")

	matches := linkedInCompanySlugPattern.FindStringSubmatch(withoutQuery)
	if len(matches) < 2 {
		return "", fmt.Errorf("invalid linkedin company url")
	}

	return strings.ToLower(matches[1]), nil
}

func normalizeLinkedInCompanyURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty linkedin url")
	}

	withoutQuery := strings.Split(trimmed, "?")[0]
	withoutQuery = strings.TrimSuffix(withoutQuery, "/")

	matches := linkedInCompanySlugPattern.FindStringSubmatch(withoutQuery)
	if len(matches) < 2 {
		return "", fmt.Errorf("invalid linkedin company url")
	}

	slug := strings.ToLower(matches[1])
	return fmt.Sprintf("https://www.linkedin.com/company/%s/", slug), nil
}
