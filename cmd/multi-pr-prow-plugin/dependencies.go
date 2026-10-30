package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/markdown"
)

var (
	dependencyHeader     = regexp.MustCompile(`(?i)^Depends-On:[ \t]*(.*)$`)
	dependencyBullet     = regexp.MustCompile(`^[-*+][ \t]+(.*)$`)
	pullRequestReference = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)#([0-9]+)$`)
)

type prReference struct {
	org, repo string
	number    int
}

func (r prReference) String() string {
	return fmt.Sprintf("%s/%s#%d", r.org, r.repo, r.number)
}

func referenceForPR(pr github.PullRequest) prReference {
	return prReference{strings.ToLower(pr.Base.Repo.Owner.Login), strings.ToLower(pr.Base.Repo.Name), pr.Number}
}

func parsePRReference(raw string) (prReference, error) {
	if strings.HasPrefix(raw, githubURL) {
		raw = strings.Replace(strings.TrimPrefix(raw, githubURL), "/pull/", "#", 1)
	}
	match := pullRequestReference.FindStringSubmatch(raw)
	if match == nil {
		return prReference{}, fmt.Errorf("invalid format for additional PR: %s", raw)
	}
	number, err := strconv.Atoi(match[3])
	if err != nil || number <= 0 {
		return prReference{}, fmt.Errorf("invalid PR number in: %s", raw)
	}
	return prReference{strings.ToLower(match[1]), strings.ToLower(match[2]), number}, nil
}

// dependenciesFromBody accepts inline declarations and header-plus-list sections.
// Fenced code examples are not declarations. A section ends at a non-bullet line
// or a blank line after its first item.
func dependenciesFromBody(body string) ([]prReference, error) {
	var refs []prReference
	section, hasItem := false, false
	add := func(raw string) error {
		ref, err := parsePRReference(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("invalid Depends-On entry %q: %w", raw, err)
		}
		refs = append(refs, ref)
		return nil
	}
	for _, line := range strings.Split(markdown.DropCodeBlock(body), "\n") {
		line = strings.TrimSpace(line)
		bullet := dependencyBullet.FindStringSubmatch(line)
		text := line
		if bullet != nil {
			text = strings.TrimSpace(bullet[1])
		}
		header := dependencyHeader.FindStringSubmatch(text)
		if section {
			if line == "" && !hasItem {
				continue
			}
			if bullet != nil && header == nil {
				if err := add(text); err != nil {
					return nil, err
				}
				hasItem = true
				continue
			}
			if !hasItem {
				return nil, fmt.Errorf("Depends-On section has no PR references")
			}
			section = false
		}
		if header == nil {
			continue
		}
		if strings.TrimSpace(header[1]) == "" {
			section, hasItem = true, false
		} else if err := add(header[1]); err != nil {
			return nil, err
		}
	}
	if section && !hasItem {
		return nil, fmt.Errorf("Depends-On section has no PR references")
	}
	return refs, nil
}

// resolvePRs returns a dependency-first ordering, including the origin. cache is
// shared across commands in one comment; traversal state and limits are per job.
func (s *server) resolvePRs(origin github.PullRequest, explicit []string, cache map[prReference]github.PullRequest) ([]github.PullRequest, error) {
	originRef := referenceForPR(origin)
	cache[originRef] = origin
	roots := map[prReference]bool{originRef: true}
	var requested []prReference
	for _, raw := range explicit {
		ref, err := parsePRReference(raw)
		if err != nil {
			return nil, err
		}
		roots[ref] = true
		requested = append(requested, ref)
	}
	requested = append(requested, originRef)

	const (
		visiting = 1
		visited  = 2
	)
	state := map[prReference]int{}
	// Count the origin even when it is visited after the explicit PRs.
	discovered := map[prReference]bool{originRef: true}
	var path []string
	var ordered []github.PullRequest
	var visit func(prReference) error
	visit = func(ref prReference) error {
		if state[ref] == visiting {
			return fmt.Errorf("Depends-On cycle: %s", strings.Join(append(path, ref.String()), " -> "))
		}
		if state[ref] == visited {
			return nil
		}
		if !discovered[ref] {
			if len(discovered) >= maxPRs {
				return fmt.Errorf("dependency graph exceeds the max of %d PRs (including the origin and merged dependencies)", maxPRs)
			}
			discovered[ref] = true
		}
		pr, ok := cache[ref]
		if !ok {
			fetched, err := s.ghc.GetPullRequest(ref.org, ref.repo, ref.number)
			if err != nil {
				return fmt.Errorf("couldn't get PR from GitHub: %s: %w", ref, err)
			}
			if fetched == nil {
				return fmt.Errorf("couldn't get PR from GitHub: %s: empty response", ref)
			}
			pr = *fetched
			cache[ref] = pr
		}
		if !roots[ref] {
			if pr.Merged {
				state[ref] = visited
				return nil
			}
			if pr.State == "closed" {
				return fmt.Errorf("Depends-On PR %s is closed without merging", ref)
			}
		}
		state[ref] = visiting
		path = append(path, ref.String())
		deps, err := dependenciesFromBody(pr.Body)
		if err != nil {
			return fmt.Errorf("PR %s: %w", ref, err)
		}
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return fmt.Errorf("dependency of %s: %w", ref, err)
			}
		}
		path = path[:len(path)-1]
		state[ref] = visited
		ordered = append(ordered, pr)
		return nil
	}
	for _, ref := range requested {
		if err := visit(ref); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
