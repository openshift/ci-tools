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

### DeleteNatGateway Event

1. Lambda searches for NAT instances tagged with the NAT Gateway ID
2. Extracts VPC ID from the NAT instance tags
3. Restores route table to point back to NAT Gateway (if available)
4. Terminates NAT instances
5. Deletes associated security groups

### TerminateInstances Event

The Lambda handles different instance types differently:

**Bootstrap instances:** Ignored (cleanup should not trigger on bootstrap termination)

**NAT instances** (have `ci-nat-gateway` tag): Always trigger cleanup

**Master/Worker nodes** (have `ci-nat-vpc` tag but not `ci-nat-gateway`):
1. Lambda checks if other masters are still running in the VPC
2. If other masters exist → **Skip cleanup** (likely MachineHealthCheck replacement)
3. If no other masters → **Trigger cleanup** (likely cluster teardown)

This logic distinguishes between:
- **MachineHealthCheck (MHC) replacement:** A single master is replaced while the cluster continues running
- **Cluster teardown:** All masters are terminated as the cluster is destroyed

**Race condition handling:** When multiple masters are terminated simultaneously, each Lambda invocation checks for other running masters. By the time the CloudTrail event is generated, the terminated instance's state has already changed to "shutting-down". Later Lambda invocations will see earlier-terminated masters as non-running, ensuring at least one Lambda correctly triggers cleanup.

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
| `ci-nat-gateway` | Marks NAT Gateway ID associated with a resource (NAT instances have this) |
| `ci-nat-instance` | Instance ID that updated a route table |
| `ci-nat-vpc` | VPC ID for the resource (added to masters when they start) |
| `ci-nat-public-subnet` | Public subnet ID |
| `ci-nat-private-route-table` | Route table the instance is configured to update |
| `ci-nat-replace` | Marker that route replacement is enabled (set by CI infrastructure) |

### ci-nat-replace Tag Values

| Value | Meaning | Lambda Action |
|-------|---------|---------------|
| `true` | Standalone IPI cluster, eligible for NAT replacement | Creates NAT instances |
| `false_job_uses_non_standalone_install_topology` | Non-standalone topology (HyperShift, SNO, etc.) | Ignored |
| `false` or missing | Not eligible | Ignored |

**Important:** The `ci-nat-replace` tag is set by CI infrastructure (prow jobs, cluster pools), not by the Lambda. The Lambda only reads this tag to decide whether to act.

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

### Check v1.5 MHC Detection in Logs

```bash
# Count MHC replacements detected (cleanup skipped)
aws --profile <profile> logs filter-log-events \
  --log-group-name use-nat-instance-log-group \
  --filter-pattern '"other masters still running"' \
  --start-time $(( $(date +%s) - 3600 ))000 \
  --region us-east-1 \
  --query 'events[*].message' --output text | grep -c "other masters"

# Count cluster teardowns detected (cleanup triggered)
aws --profile <profile> logs filter-log-events \
  --log-group-name use-nat-instance-log-group \
  --filter-pattern '"no other masters running"' \
  --start-time $(( $(date +%s) - 3600 ))000 \
  --region us-east-1 \
  --query 'events[*].message' --output text | grep -c "no other masters"

# Sample MHC detection messages
aws --profile <profile> logs filter-log-events \
  --log-group-name use-nat-instance-log-group \
  --filter-pattern '"other masters still running"' \
  --start-time $(( $(date +%s) - 3600 ))000 \
  --region us-east-1 \
  --query 'events[*].message' --output text | tr '\t' '\n' | head -5
```

### Verify Lambda Version

```bash
# Check version in recent logs
aws --profile <profile> logs filter-log-events \
  --log-group-name use-nat-instance-log-group \
  --filter-pattern '"[v1."' \
  --start-time $(( $(date +%s) - 3600 ))000 \
  --region us-east-1 \
  --query 'events[0].message' --output text | grep -o '\[v1\.[0-9]*\]'
```

## Version History

- **v1.0:** Initial implementation with dynamic instance profiles
- **v1.1:** Bug fixes for instance profile cleanup
- **v1.2:** Static instance profile via CloudFormation, improved cleanup
- **v1.3:** Fixed race condition - wait for 0.0.0.0/0 route before replacing
- **v1.3.1:** Added `ec2:DescribeRouteTables` to NAT instance role (required by v1.3 UserData script)
- **v1.4:** Improved DeleteNatGateway handling - search for NAT instances by tag instead of describing the NAT gateway (avoids race condition when NAT gateway is already deleted)
- **v1.5:** Fixed MachineHealthCheck false positive - master terminations now check if other masters are still running before triggering cleanup. This prevents NAT instances from being incorrectly terminated when MHC replaces a single master while the cluster is still running.

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

### MachineHealthCheck Causes False Positive Cleanup (v1.4 Bug, Fixed in v1.5)

**Incident:** Approximately 7% of running clusters at any given time experience MachineHealthCheck (MHC) remediation, where a failing master node is replaced with a new one. The v1.4 Lambda incorrectly treated master terminations as cluster teardown signals.

**Root Cause Analysis:**
1. When a master is created, the Lambda adds a `ci-nat-vpc` tag to track which VPC it belongs to
2. When any instance with `ci-nat-vpc` tag is terminated, v1.4 triggered cleanup
3. MHC terminates the old master → Lambda sees the tag → triggers cleanup → terminates NAT instances
4. The cluster continues running but now uses expensive NAT gateways instead of NAT instances

**Detection:** 
- Clusters with replacement masters (naming pattern `master-XXXXX-N` instead of `master-0/1/2`) had no NAT instances
- CloudTrail showed machine-api-controller terminating masters (not cluster teardown)
- NAT instance placement ratio dropped below expected levels

**Investigation commands:**
```bash
# Find clusters with replacement masters
aws --profile <profile> ec2 describe-instances \
  --filters "Name=instance-state-name,Values=running" \
  --query 'Reservations[*].Instances[*].Tags[?Key==`Name`].Value' \
  --output text --region us-east-1 | tr '\t' '\n' | grep -E 'master-[a-z0-9]{5}-[0-9]'

# Check who terminated an instance
aws --profile <profile> cloudtrail lookup-events \
  --lookup-attributes AttributeKey=ResourceName,AttributeValue=<instance-id> \
  --region us-east-1 --query 'Events[*].[EventName,Username]' --output table
```

**Fix (v1.5):** Before triggering cleanup on master termination, check if other masters are still running in the VPC:
- If other masters exist → Skip cleanup (MHC replacement)
- If no other masters → Trigger cleanup (cluster teardown)

**Race Condition Analysis:** When all masters are terminated simultaneously during cluster teardown, each Lambda invocation checks for other running masters. Since the TerminateInstances CloudTrail event is generated *after* the instance state changes to "shutting-down", later Lambda invocations will see earlier-terminated masters as non-running. At least one Lambda invocation will correctly see no other running masters and trigger cleanup.

**Lesson:** Master node termination is not always cluster teardown. MachineHealthCheck, manual remediation, or other scenarios can terminate masters while the cluster continues running. Always verify the cluster state before triggering destructive cleanup operations.

## Non-Standalone Topology Clusters

Some clusters have the tag `ci-nat-replace=false_job_uses_non_standalone_install_topology` instead of `ci-nat-replace=true`. These clusters are NOT processed by the Lambda.

### What "Non-Standalone" Means

The tag value is set by CI infrastructure (prow jobs, cluster pools) based on the install topology. Non-standalone topologies may include:
- Hosted Control Plane (HyperShift) management clusters
- Compact/Single-Node OpenShift (SNO) clusters
- Multi-cluster test scenarios
- Other non-standard install configurations

### Infrastructure Assessment

**Key finding:** Non-standalone clusters have **identical AWS infrastructure** to standalone clusters:
- Same subnet naming pattern: `{cluster}-subnet-{public|private}-{az}`
- Same NAT gateway setup (one per AZ)
- Same route table structure
- Same instance tagging

The Lambda's logic would work correctly for these clusters. The only reason they're excluded is the `ci-nat-replace` tag value set by CI infrastructure.

### Statistics (typical snapshot)

| Tag Value | Instances | % of Total |
|-----------|-----------|------------|
| `true` | ~85% | Eligible, NAT replaced |
| `false_job_uses_non_standalone_install_topology` | ~10-15% | Not eligible |
| No tag / other | ~5% | Not eligible |

### Potential Expansion

To enable NAT instance replacement for non-standalone clusters, the CI infrastructure would need to set `ci-nat-replace=true`. The Lambda itself requires no changes - it already handles the standard AWS networking topology these clusters use.

