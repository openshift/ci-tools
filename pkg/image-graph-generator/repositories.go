package imagegraphgenerator

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

func (o *Operator) resolveRepository(org, repo string) (map[string]interface{}, error) {
	resolvedOrg, err := o.resolveOrganization(org)
	if err != nil {
		return nil, err
	}

	if _, ok := o.repositories[fmt.Sprintf("%s/%s", org, repo)]; !ok {
		if err := o.addRepositoryRef(org, repo); err != nil {
			return nil, err
		}
	}

	return map[string]interface{}{
		"id":           o.repositories[fmt.Sprintf("%s/%s", org, repo)],
		"name":         repo,
		"organization": resolvedOrg,
	}, nil
}

func (o *Operator) loadRepositories() error {
	var m struct {
		QueryRepository []struct {
			ID           string `graphql:"id"`
			Name         string `graphql:"name"`
			Organization struct {
				Name string `graphql:"name"`
			} `graphql:"organization"`
		} `graphql:"queryRepository"`
	}

	if err := o.c.Query(context.Background(), &m, nil); err != nil {
		return err
	}

	for _, repo := range m.QueryRepository {
		o.repositories[fmt.Sprintf("%s/%s", repo.Organization.Name, repo.Name)] = repo.ID
	}
	return nil
}

func (o *Operator) addRepositoryRef(org, repo string) error {
	if _, ok := o.repositories[repo]; ok {
		return nil
	}

	logrus.WithField("repository", repo).Info("Adding repository...")
	var m struct {
		AddRepository struct {
			NumUIDs    int `graphql:"numUids"`
			Repository []struct {
				ID           string `graphql:"id"`
				Name         string `graphql:"name"`
				Organization struct {
					Name string `graphql:"name"`
				} `graphql:"organization"`
			} `graphql:"repository"`
		} `graphql:"addRepository(input: $input)"`
	}

	type AddRepositoryInput map[string]interface{}
	type OrganizationRef map[string]interface{}
	input := AddRepositoryInput{
		"name": repo,
		"organization": OrganizationRef{
			"id": o.organizations[org],
		},
	}

	vars := map[string]interface{}{
		"input": []AddRepositoryInput{input},
	}

	if err := o.c.Mutate(context.Background(), &m, vars); err != nil {
		return err
	}

	if len(m.AddRepository.Repository) > 0 {
		name := fmt.Sprintf("%s/%s", m.AddRepository.Repository[0].Organization.Name, m.AddRepository.Repository[0].Name)
		o.repositories[name] = m.AddRepository.Repository[0].ID
	}

	return nil
}
