# NAT Gateway Cost Reduction - Agent Reference

This document provides comprehensive information for AI agents working with the NAT Gateway cost reduction solution.

## Overview

This solution replaces expensive AWS NAT Gateways with low-cost NAT instances for ephemeral OpenShift CI clusters. NAT Gateway traffic costs several cents per GB, while NAT instance traffic is nearly free (only the small EC2 instance and its public IP are charged).

### How It Works

1. **EventBridge** monitors EC2 API calls via CloudTrail
2. When a `CreateNatGateway` event is detected, a **Lambda function** is triggered
3. The Lambda creates a small EC2 instance (NAT instance) in the same AZ
4. The NAT instance's **userData script** updates the private route table to route `0.0.0.0/0` traffic through itself instead of the NAT Gateway
5. When the cluster is destroyed (`TerminateInstances` or `DeleteNatGateway` events), the Lambda cleans up the NAT instance

## AWS Accounts

The solution is deployed to these AWS accounts:

| Profile Name | Account ID | Notes |
|--------------|------------|-------|
| `openshift-ci-1` | 460538899914 | Primary CI account |
| `openshift-ci-2` | 892173657978 | CI account |
| `openshift-ci-3` | 130757279292 | CI account |
| `openshift-ci-4` | 387344700001 | CI account |
| `openshift-ci-5` | 677273281401 | CI account |

### AWS Profile Setup

**Important:** The user is responsible for configuring AWS CLI profiles on their local machine. These profiles must be set up in `~/.aws/config` and `~/.aws/credentials` (or via SSO) before any deployment or monitoring operations.

**AI agents should verify profiles before use:**

```bash
# Test that a profile exists and has valid credentials
aws --profile <profile> sts get-caller-identity

# Test all profiles at once
for PROFILE in openshift-ci-1 openshift-ci-2 openshift-ci-3 openshift-ci-4 openshift-ci-5; do
  echo -n "$PROFILE: "
  aws --profile "$PROFILE" sts get-caller-identity --query 'Account' --output text 2>&1 || echo "FAILED"
done
```

If credentials are expired or missing, ask the user to refresh their AWS authentication before proceeding.

## File Structure

```
hack/reduce_nat_gateway_cost/
├── replace_nat_with_nat_instance.py  # Main Lambda function code
├── use-nat-instance.yaml             # CloudFormation template for us-east-1
├── use-nat-instance-forwarders.yaml  # CloudFormation template for other regions
├── deploy.sh                          # Deployment script
├── monitor_resources.py               # Python monitoring script
├── lambda.zip                         # Packaged Lambda (auto-generated)
└── AGENTS.md                          # This file
```

## Key Resources Created

### In us-east-1 (Main Region)

| Resource | Name | Purpose |
|----------|------|---------|
| Lambda Function | `use-nat-instance-function` | Main logic - creates NAT instances, updates routes |
| IAM Role | `use-nat-instance-function-role` | Lambda execution permissions |
| IAM Role | `use-nat-instance-role` | NAT instance permissions (modify routes) |
| IAM Instance Profile | `use-nat-instance-profile` | Attached to NAT instances |
| EventBridge Rule | `use-nat-instance-event-rule` | Triggers Lambda on EC2 events |
| IAM Role | `use-nat-instance-execution-role` | EventBridge to invoke Lambda |
| CloudWatch Log Group | `use-nat-instance-log-group` | Lambda logs (14 day retention) |

### In us-east-2, us-west-1, us-west-2 (Forwarder Regions)

| Resource | Name | Purpose |
|----------|------|---------|
| EventBridge Rule | `use-nat-instance-forward-event-rule` | Forwards events to us-east-1 |
| IAM Role | `use-nat-instance-forward-role-{region}` | Permission to forward events |

## Deployment

### Deploy to an Account

```bash
cd hack/reduce_nat_gateway_cost
./deploy.sh <aws-profile>

# Example:
./deploy.sh openshift-ci-1
```

### Deploy to All Accounts

```bash
for PROFILE in openshift-ci-1 openshift-ci-2 openshift-ci-3 openshift-ci-4 openshift-ci-5; do
  ./deploy.sh "$PROFILE"
done
```

### Destroy (Disable NAT Instance Replacement)

```bash
./deploy.sh <aws-profile> destroy
```

**Important:** Resources have `DeletionPolicy: Retain` to prevent accidental deletion. The `destroy` command explicitly deletes the Lambda function (the critical resource) but retains IAM roles and other resources.

### Update Lambda Code Only

If you only changed `replace_nat_with_nat_instance.py`:

```bash
cd hack/reduce_nat_gateway_cost
rm -f lambda.zip
zip -r lambda.zip replace_nat_with_nat_instance.py
aws --profile <aws-profile> lambda update-function-code \
  --function-name use-nat-instance-function \
  --zip-file fileb://lambda.zip \
  --region us-east-1
```

## Monitoring

### Run the Monitor Script

```bash
cd hack/reduce_nat_gateway_cost
python3 monitor_resources.py --once      # Single check
python3 monitor_resources.py             # Continuous monitoring (5 min intervals)
python3 monitor_resources.py --alarm     # With audio alarm on issues
```

### What the Monitor Checks

1. **Expected Resources Exist**: Lambda, IAM roles, instance profile, EventBridge rules
2. **Orphaned IAM Resources**: Instance profiles/roles with `Created-` prefix (legacy)
3. **Orphaned EC2 Resources**: Security groups and NAT instances where VPC no longer exists
4. **Instance Profile Count**: Warns if ≥500 (AWS limit is 1000)
5. **NAT Instance Age**: Alerts if any NAT instance is >8 hours old
6. **Lambda Errors**: CloudWatch metrics for errors in last 8 hours
7. **NAT Instance Effectiveness**: Percentage of NAT instances that successfully updated route tables

### Check Lambda Logs

```bash
aws --profile <profile> logs filter-log-events \
  --log-group-name use-nat-instance-log-group \
  --filter-pattern "ERROR" \
  --start-time $(( $(date +%s) - 86400 ))000 \
  --region us-east-1
```

### Verify Lambda is Working

```bash
aws --profile <profile> lambda get-function \
  --function-name use-nat-instance-function \
  --region us-east-1 \
  --query '{State: Configuration.State, LastModified: Configuration.LastModified, CodeSha256: Configuration.CodeSha256}'
```

## Lambda Event Flow

### CreateNatGateway Event

1. Lambda receives event with NAT Gateway details
2. Finds the public subnet where NAT Gateway was created
3. Finds the corresponding private subnet (same VPC/AZ, name contains `-private`)
4. Creates a security group for the NAT instance
5. Launches a NAT instance (t4g.nano ARM64) with userData script
6. Tags the NAT Gateway, route table, and instance for tracking
7. The userData script (on the instance):
   - Enables IP forwarding and NAT via iptables
   - **Waits for 0.0.0.0/0 route to exist** (up to 5 minutes)
   - Replaces the route to point to itself
   - Tags the route table with the instance ID

### DeleteNatGateway / TerminateInstances Events

1. Lambda identifies affected VPC
2. Finds NAT instances tagged with `ci-nat-gateway`
3. Restores route table to point back to NAT Gateway (if available)
4. Terminates NAT instances
5. Deletes associated security groups

## Common Issues and Fixes

### Issue: "Unable to import module 'replace_nat_with_nat_instance'"

**Cause:** Lambda code not uploaded after CloudFormation deploy.

**Fix:** Upload the Lambda code:
```bash
rm -f lambda.zip
zip -r lambda.zip replace_nat_with_nat_instance.py
aws --profile <profile> lambda update-function-code \
  --function-name use-nat-instance-function \
  --zip-file fileb://lambda.zip \
  --region us-east-1
```

### Issue: "There is no route defined for '0.0.0.0/0' in the route table"

**Cause:** Race condition - NAT instance starts before cluster installer creates the route.

**Fix:** v1.3+ of the Lambda includes a retry loop that waits for the route to exist.

### Issue: NAT instances not being cleaned up

**Cause:** Lambda was broken during the cleanup event, or forwarder not working.

**Fix:** Manually terminate orphaned instances:
```bash
aws --profile <profile> ec2 terminate-instances --instance-ids <ids> --region <region>
```

### Issue: Stack in ROLLBACK_COMPLETE state

**Cause:** Previous deployment failed.

**Fix:** Delete the stack and redeploy:
```bash
aws --profile <profile> cloudformation delete-stack --stack-name use-nat-instance --region us-east-1
aws --profile <profile> cloudformation wait stack-delete-complete --stack-name use-nat-instance --region us-east-1
./deploy.sh <profile>
```

### Issue: ResourceExistenceCheck failure during deploy

**Cause:** Resources with `DeletionPolicy: Retain` exist outside CloudFormation.

**Fix:** Delete the retained resources manually, then redeploy:
```bash
# Delete Lambda, roles, instance profile, event rules, log group
# Then redeploy
./deploy.sh <profile>
```

### Issue: NAT instances failing with "UnauthorizedOperation" on ec2:DescribeRouteTables

**Cause:** The UserData script uses AWS CLI commands that require permissions not granted to `use-nat-instance-role`.

**Fix:** Update the IAM policy in all accounts:
```bash
for PROFILE in openshift-ci-1 openshift-ci-2 openshift-ci-3 openshift-ci-4 openshift-ci-5; do
  aws --profile "$PROFILE" iam put-role-policy \
    --role-name use-nat-instance-role \
    --policy-name nat-instance-policy \
    --policy-document '{
      "Version": "2012-10-17",
      "Statement": [
        {"Effect": "Allow", "Action": ["ec2:ReplaceRoute", "ec2:DescribeRouteTables"], "Resource": "*"},
        {"Effect": "Allow", "Action": ["ec2:CreateTags"], "Resource": "arn:aws:ec2:*:*:route-table/*"},
        {"Effect": "Allow", "Action": ["ec2:ModifyInstanceAttribute"], "Resource": "*"}
      ]
    }'
done
```

Also update `use-nat-instance.yaml` so future deployments include the fix.

## Tags Used

| Tag Key | Purpose |
|---------|---------|
| `ci-nat-gateway` | Marks NAT Gateway ID associated with a resource |
| `ci-nat-instance` | Instance ID that updated a route table |
| `ci-nat-vpc` | VPC ID for the resource |
| `ci-nat-public-subnet` | Public subnet ID |
| `ci-nat-private-route-table` | Route table the instance is configured to update |
| `ci-nat-replace` | Marker that route replacement is enabled |

## NAT Instance Details

- **AMI:** Amazon Linux 2 (latest, ARM64)
- **Instance Types (tried in order):**
  1. `t4g.nano` (preferred - cheapest ARM)
  2. `t4g.micro`
  3. `t3.nano`
  4. `t3.micro`
- **Instance Profile:** `use-nat-instance-profile`
- **Security Group:** Named `{subnet-name}-ci-nat-sg`, allows inbound from private subnet CIDR

## Useful Commands

### List NAT Instances in an Account

```bash
aws --profile <profile> ec2 describe-instances \
  --filters "Name=tag-key,Values=ci-nat-gateway" "Name=instance-state-name,Values=running" \
  --region <region> \
  --query 'Reservations[*].Instances[*].[InstanceId,VpcId,LaunchTime,Tags[?Key==`Name`].Value|[0]]' \
  --output table
```

### Check Route Table for NAT Instance Route

```bash
aws --profile <profile> ec2 describe-route-tables \
  --route-table-ids <rtb-id> \
  --region <region> \
  --query 'RouteTables[0].Routes[?DestinationCidrBlock==`0.0.0.0/0`]'
```

### Get NAT Instance Console Output (for debugging userData)

```bash
aws --profile <profile> ec2 get-console-output \
  --instance-id <instance-id> \
  --region <region> \
  --output text
```

### Count Instance Profiles

```bash
aws --profile <profile> iam list-instance-profiles --no-paginate \
  --query 'InstanceProfiles[*].InstanceProfileName' --output text | wc -w
```

## Version History

- **v1.0:** Initial implementation with dynamic instance profiles
- **v1.1:** Bug fixes for instance profile cleanup
- **v1.2:** Static instance profile via CloudFormation, improved cleanup
- **v1.3:** Fixed race condition - wait for 0.0.0.0/0 route before replacing
- **v1.3.1:** Added `ec2:DescribeRouteTables` to NAT instance role (required by v1.3 UserData script)

## Important Notes

1. **PG&E Cloud Ops Pruner:** If any **new resources** (compared to what currently exists) are added to the CloudFormation templates, the PG&E Cloud Ops pruner will likely automatically delete them from the AWS account shortly after creation. A request must be filed with Cloud Ops to have new resource types whitelisted/preserved before deploying CloudFormation changes that create new resources.

2. **DeletionPolicy: Retain:** Most resources are retained on stack deletion to prevent accidental service disruption.

3. **Lambda Timeout:** 10 minutes (600 seconds) - sufficient for all operations.

4. **Event Retry:** EventBridge retries once with max 5 minute event age.

5. **Regions:** Lambda runs in us-east-1; forwarders in us-east-2, us-west-1, us-west-2.

## Lessons Learned

### UserData Scripts Require Corresponding IAM Permissions

**Incident (v1.3):** The UserData script was updated to call `aws ec2 describe-route-tables` to wait for the `0.0.0.0/0` route to exist before replacing it. However, the `use-nat-instance-role` IAM policy was not updated to include `ec2:DescribeRouteTables` permission.

**Result:** All NAT instances launched after the v1.3 deployment failed to update their route tables. The UserData script's retry loop ran for 5 minutes, logging `UnauthorizedOperation` errors on every attempt, then gave up.

**Detection:** The monitoring script showed NAT instance effectiveness dropping from ~85% to ~23%.

**Fix:** Added `ec2:DescribeRouteTables` permission to the `use-nat-instance-role` policy in `use-nat-instance.yaml` and manually updated the policy in all accounts using `aws iam put-role-policy`.

**Lesson:** When modifying the UserData script to call AWS APIs, always verify that the NAT instance's IAM role (`use-nat-instance-role`) has the required permissions. The role's policy is defined in `use-nat-instance.yaml` under `NatInstanceRole.Policies`.

