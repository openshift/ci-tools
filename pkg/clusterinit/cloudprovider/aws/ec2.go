package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awstypes "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
	"k8s.io/utils/ptr"
)

func SubnetIdForMachineSet(ctx context.Context, client awstypes.EC2Client, infraId, az string) (string, error) {
	paginator := ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{Filters: []ec2types.Filter{
		{Name: ptr.To("availability-zone"), Values: []string{az}},
		{Name: ptr.To("tag-key"), Values: []string{fmt.Sprintf("kubernetes.io/cluster/%s", infraId)}},
	}})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("describe subnets %s %s: %w", infraId, az, err)
		}
		if len(page.Subnets) > 0 {
			if page.Subnets[0].SubnetId == nil {
				return "", fmt.Errorf("subnet %s %s: subnetId is nil", infraId, az)
			}
			return *page.Subnets[0].SubnetId, nil
		}
	}
	return "", fmt.Errorf("%s %s: no subnet ids found", infraId, az)
}

func SecurityGroupNamesForMachineSet(ctx context.Context, client awstypes.EC2Client, infraId string, roles ...string) ([]string, error) {
	paginator := ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{Filters: []ec2types.Filter{
		{Name: ptr.To("tag:sigs.k8s.io/cluster-api-provider-aws/role"), Values: roles},
		{Name: ptr.To("tag-key"), Values: []string{fmt.Sprintf("sigs.k8s.io/cluster-api-provider-aws/cluster/%s", infraId)}},
	}})
	names := make([]string, 0)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe security groups %s: %w", infraId, err)
		}
		for i := range page.SecurityGroups {
			if page.SecurityGroups[i].GroupName != nil {
				names = append(names, *page.SecurityGroups[i].GroupName)
			}
		}
	}
	return names, nil
}
