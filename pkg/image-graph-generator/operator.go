package imagegraphgenerator

import (
	"fmt"

	"github.com/shurcooL/graphql"
)

type Operator struct {
	c             *graphql.Client
	organizations map[string]string
	repositories  map[string]string
	branches      map[string]string
	images        map[string]string
}

func NewOperator(c *graphql.Client) *Operator {
	return &Operator{
		c:             c,
		organizations: make(map[string]string),
		repositories:  make(map[string]string),
		branches:      make(map[string]string),
		images:        make(map[string]string),
	}
}

func (o *Operator) Load() error {
	if err := o.loadImages(); err != nil {
		return fmt.Errorf("couldn't get all images: %w", err)
	}

	if err := o.loadOrganizations(); err != nil {
		return fmt.Errorf("couldn't get organizations: %w", err)
	}

	if err := o.loadRepositories(); err != nil {
		return fmt.Errorf("couldn't get repositories: %w", err)
	}

	if err := o.loadBranches(); err != nil {
		return fmt.Errorf("couldn't get branches: %w", err)
	}
	return nil
}
