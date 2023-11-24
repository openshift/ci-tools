package imagegraphgenerator

import (
	"context"

	"github.com/sirupsen/logrus"
)

func (o *Operator) addOrganizationRef(org string) error {
	if _, ok := o.organizations[org]; ok {
		return nil
	}

	logrus.WithField("organization", org).Info("Adding organization...")

	var m struct {
		AddOrganization struct {
			NumUIDs      int `graphql:"numUids"`
			Organization []struct {
				ID string `graphql:"id"`
			} `graphql:"organization"`
		} `graphql:"addOrganization(input: $input)"`
	}

	type AddOrganizationInput map[string]interface{}
	input := AddOrganizationInput{
		"name": org,
	}

	vars := map[string]interface{}{
		"input": []AddOrganizationInput{input},
	}

	if err := o.c.Mutate(context.Background(), &m, vars); err != nil {
		return err
	}

	if len(m.AddOrganization.Organization) > 0 {
		o.organizations[org] = m.AddOrganization.Organization[0].ID
	}

	return nil
}

func (o *Operator) resolveOrganization(org string) (map[string]interface{}, error) {
	if _, ok := o.organizations[org]; !ok {
		if err := o.addOrganizationRef(org); err != nil {
			return nil, err
		}
	}

	return map[string]interface{}{
		"id":   o.organizations[org],
		"name": org,
	}, nil
}

func (o *Operator) loadOrganizations() error {
	var m struct {
		QueryOrganization []struct {
			ID   string `graphql:"id"`
			Name string `graphql:"name"`
		} `graphql:"queryOrganization"`
	}

	if err := o.c.Query(context.Background(), &m, nil); err != nil {
		return err
	}

	for _, org := range m.QueryOrganization {
		o.organizations[org.Name] = org.ID
	}
	return nil
}
