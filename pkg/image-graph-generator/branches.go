package imagegraphgenerator

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

type BranchRef struct {
	ID string `graphql:"id"`
}

func (o *Operator) Branches() map[string]string {
	return o.branches
}

func (o *Operator) loadBranches() error {
	var m struct {
		QueryBranch []struct {
			ID         string `graphql:"id"`
			Name       string `graphql:"name"`
			Repository struct {
				Name         string `graphql:"name"`
				Organization struct {
					Name string `graphql:"name"`
				} `graphql:"organization"`
			} `graphql:"repository"`
		} `graphql:"queryBranch"`
	}

	if err := o.c.Query(context.Background(), &m, nil); err != nil {
		return err
	}

	for _, branch := range m.QueryBranch {
		name := fmt.Sprintf("%s/%s:%s", branch.Repository.Organization.Name, branch.Repository.Name, branch.Name)
		o.branches[name] = branch.ID
	}
	return nil
}

func (o *Operator) AddBranchRef(org, repo, branch string) error {
	if _, ok := o.branches[fmt.Sprintf("%s/%s:%s", org, repo, branch)]; ok {
		return nil
	}

	logrus.WithFields(logrus.Fields{"org": org, "repo": repo, "branch": branch}).Info("Adding branch...")
	var m struct {
		AddBranch struct {
			NumUIDs int `graphql:"numUids"`
			Branch  []struct {
				ID         string `graphql:"id"`
				Name       string `graphql:"name"`
				Repository struct {
					Name         string `graphql:"name"`
					Organization struct {
						Name string `graphql:"name"`
					} `graphql:"organization"`
				} `graphql:"repository"`
			} `graphql:"branch"`
		} `graphql:"addBranch(input: $input)"`
	}

	type AddBranchInput map[string]interface{}

	resolvedRepo, err := o.resolveRepository(org, repo)
	if err != nil {
		return err
	}

	input := AddBranchInput{
		"name":       branch,
		"repository": resolvedRepo,
	}

	vars := map[string]interface{}{
		"input": []AddBranchInput{input},
	}

	if err := o.c.Mutate(context.Background(), &m, vars); err != nil {
		return err
	}

	if len(m.AddBranch.Branch) > 0 {
		branch := m.AddBranch.Branch[0]
		name := fmt.Sprintf("%s/%s:%s", branch.Repository.Organization.Name, branch.Repository.Name, branch.Name)
		o.branches[name] = branch.ID
	}

	return nil
}
