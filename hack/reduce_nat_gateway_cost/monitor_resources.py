#!/usr/bin/env python3
"""
Monitor script for NAT instance resource leaks.

Checks for orphaned security groups, NAT instances, and instance profiles
across multiple AWS accounts. Also monitors NAT instance age and Lambda errors.
"""

import argparse
import subprocess
import sys
import time
from datetime import datetime, timezone, timedelta
from typing import Optional

import boto3
from botocore.exceptions import ClientError

# Configuration
PROFILES = ["openshift-ci-1", "openshift-ci-2", "openshift-ci-3", "openshift-ci-4", "openshift-ci-5"]
REGIONS = ["us-east-1", "us-east-2", "us-west-1", "us-west-2"]
CHECK_INTERVAL_SECONDS = 300  # 5 minutes
INSTANCE_PROFILE_THRESHOLD = 500
NAT_INSTANCE_AGE_THRESHOLD_HOURS = 8
NAT_INSTANCE_EFFECTIVENESS_MINUTES = 15  # Check effectiveness for instances older than this
LAMBDA_FUNCTION_NAME = "use-nat-instance-function"
LAMBDA_REGION = "us-east-1"

# Expected resources that should exist in each account
EXPECTED_INSTANCE_PROFILE = "use-nat-instance-profile"
EXPECTED_ROLES = ["use-nat-instance-role", "use-nat-instance-function-role"]
EXPECTED_EVENTBRIDGE_RULE = "use-nat-instance-event-rule"
EXPECTED_FORWARDER_RULE = "use-nat-instance-forward-event-rule"
FORWARDER_REGIONS = ["us-east-2", "us-west-1", "us-west-2"]

# ANSI colors
RED = "\033[0;31m"
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
CYAN = "\033[0;36m"
NC = "\033[0m"  # No Color


def format_duration(td: timedelta) -> str:
    """Format a timedelta as a human-readable duration string like '2h3m' or '45m'."""
    total_seconds = int(td.total_seconds())
    hours, remainder = divmod(total_seconds, 3600)
    minutes, _ = divmod(remainder, 60)
    
    if hours > 0:
        return f"{hours}h{minutes}m"
    else:
        return f"{minutes}m"


def play_alarm():
    """Play an alarm sound using available system utilities."""
    try:
        # Try PulseAudio (Linux)
        for _ in range(3):
            result = subprocess.run(
                ["paplay", "/usr/share/sounds/freedesktop/stereo/phone-incoming-call.oga"],
                capture_output=True,
                timeout=5,
            )
            if result.returncode != 0:
                subprocess.run(
                    ["paplay", "/usr/share/sounds/gnome/default/alerts/drip.ogg"],
                    capture_output=True,
                    timeout=5,
                )
            time.sleep(0.5)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        try:
            # Try macOS
            for _ in range(3):
                subprocess.run(
                    ["afplay", "/System/Library/Sounds/Ping.aiff"],
                    capture_output=True,
                    timeout=5,
                )
                time.sleep(0.5)
        except (FileNotFoundError, subprocess.TimeoutExpired):
            # Terminal bell as last resort
            for _ in range(5):
                print("\a", end="", flush=True)
                time.sleep(0.3)


def get_session(profile: str, region: str = "us-east-1") -> boto3.Session:
    """Create a boto3 session for the given profile and region."""
    return boto3.Session(profile_name=profile, region_name=region)


def vpc_exists(ec2_client, vpc_id: str) -> bool:
    """Check if a VPC exists."""
    try:
        ec2_client.describe_vpcs(VpcIds=[vpc_id])
        return True
    except ClientError as e:
        if "InvalidVpcID.NotFound" in str(e):
            return False
        raise


def check_orphaned_ec2_resources(profile: str, region: str) -> tuple[list[str], list[dict]]:
    """
    Check for orphaned security groups and NAT instances in a profile/region.
    
    Returns:
        Tuple of (issues list, nat_instances list with details)
    """
    issues = []
    nat_instances = []
    
    session = get_session(profile, region)
    ec2_client = session.client("ec2")
    
    # Check for security groups with ci-nat-gateway tag
    try:
        response = ec2_client.describe_security_groups(
            Filters=[{"Name": "tag-key", "Values": ["ci-nat-gateway"]}]
        )
        for sg in response.get("SecurityGroups", []):
            sg_id = sg["GroupId"]
            sg_name = sg.get("GroupName", "unknown")
            vpc_id = sg.get("VpcId", "")
            if vpc_id and not vpc_exists(ec2_client, vpc_id):
                issues.append(f"  ORPHANED SECURITY GROUP: {sg_id} ({sg_name}) - VPC {vpc_id} no longer exists")
    except ClientError as e:
        issues.append(f"  ERROR checking security groups: {e}")
    
    # Check for NAT instances with ci-nat-gateway tag
    try:
        response = ec2_client.describe_instances(
            Filters=[
                {"Name": "tag-key", "Values": ["ci-nat-gateway"]},
                {"Name": "instance-state-name", "Values": ["running", "pending", "stopping", "stopped"]},
            ]
        )
        for reservation in response.get("Reservations", []):
            for instance in reservation.get("Instances", []):
                instance_id = instance["InstanceId"]
                vpc_id = instance.get("VpcId", "")
                launch_time = instance.get("LaunchTime")
                
                # Get instance name from tags
                instance_name = "unknown"
                for tag in instance.get("Tags", []):
                    if tag["Key"] == "Name":
                        instance_name = tag["Value"]
                        break
                
                nat_instances.append({
                    "instance_id": instance_id,
                    "instance_name": instance_name,
                    "vpc_id": vpc_id,
                    "launch_time": launch_time,
                    "region": region,
                    "profile": profile,
                })
                
                # Check if VPC exists
                if vpc_id and not vpc_exists(ec2_client, vpc_id):
                    issues.append(f"  ORPHANED NAT INSTANCE: {instance_id} ({instance_name}) - VPC {vpc_id} no longer exists")
    except ClientError as e:
        issues.append(f"  ERROR checking NAT instances: {e}")
    
    return issues, nat_instances


def check_orphaned_iam_resources(profile: str) -> list[str]:
    """Check for orphaned instance profiles and roles with Created- prefix."""
    issues = []
    
    session = get_session(profile)
    iam_client = session.client("iam")
    
    # Check for instance profiles with Created- prefix
    try:
        paginator = iam_client.get_paginator("list_instance_profiles")
        for page in paginator.paginate():
            for ip in page.get("InstanceProfiles", []):
                if "Created-" in ip["InstanceProfileName"]:
                    issues.append(f"  ORPHANED INSTANCE PROFILE: {ip['InstanceProfileName']}")
    except ClientError as e:
        issues.append(f"  ERROR checking instance profiles: {e}")
    
    # Check for roles with Created- prefix
    try:
        paginator = iam_client.get_paginator("list_roles")
        for page in paginator.paginate():
            for role in page.get("Roles", []):
                if "Created-" in role["RoleName"]:
                    issues.append(f"  ORPHANED ROLE: {role['RoleName']}")
    except ClientError as e:
        issues.append(f"  ERROR checking roles: {e}")
    
    return issues


def check_instance_profile_count(profile: str) -> tuple[int, Optional[str]]:
    """Check total instance profile count and return warning if over threshold."""
    session = get_session(profile)
    iam_client = session.client("iam")
    
    count = 0
    try:
        paginator = iam_client.get_paginator("list_instance_profiles")
        for page in paginator.paginate():
            count += len(page.get("InstanceProfiles", []))
    except ClientError as e:
        return 0, f"  ERROR counting instance profiles: {e}"
    
    if count >= INSTANCE_PROFILE_THRESHOLD:
        return count, f"  WARNING: {count} instance profiles (threshold: {INSTANCE_PROFILE_THRESHOLD})"
    
    return count, None


def check_nat_instance_age(nat_instances: list[dict]) -> list[str]:
    """Check if any NAT instances are older than the threshold."""
    issues = []
    now = datetime.now(timezone.utc)
    threshold = timedelta(hours=NAT_INSTANCE_AGE_THRESHOLD_HOURS)
    
    for instance in nat_instances:
        launch_time = instance.get("launch_time")
        if launch_time:
            age = now - launch_time
            if age > threshold:
                hours = age.total_seconds() / 3600
                issues.append(
                    f"  OLD NAT INSTANCE: {instance['instance_id']} ({instance['instance_name']}) "
                    f"in {instance['profile']}/{instance['region']} - running for {hours:.1f} hours"
                )
    
    return issues


def get_lambda_error_count(profile: str, hours: int = 8) -> tuple[int, Optional[str]]:
    """
    Get the number of Lambda errors in the last N hours using CloudWatch metrics.
    
    Uses the Errors metric which is automatically published by Lambda.
    """
    session = get_session(profile, LAMBDA_REGION)
    cloudwatch_client = session.client("cloudwatch")
    
    end_time = datetime.now(timezone.utc)
    start_time = end_time - timedelta(hours=hours)
    
    try:
        response = cloudwatch_client.get_metric_statistics(
            Namespace="AWS/Lambda",
            MetricName="Errors",
            Dimensions=[
                {"Name": "FunctionName", "Value": LAMBDA_FUNCTION_NAME},
            ],
            StartTime=start_time,
            EndTime=end_time,
            Period=3600 * hours,  # One period covering the entire time range
            Statistics=["Sum"],
        )
        
        datapoints = response.get("Datapoints", [])
        if datapoints:
            return int(datapoints[0].get("Sum", 0)), None
        return 0, None
    except ClientError as e:
        return 0, f"ERROR getting Lambda metrics: {e}"


def get_lambda_last_modified(profile: str) -> tuple[Optional[datetime], Optional[str]]:
    """
    Get the last modified time of the Lambda function.
    
    Returns:
        Tuple of (last_modified datetime in UTC, error message if any)
    """
    session = get_session(profile, LAMBDA_REGION)
    lambda_client = session.client("lambda")
    
    try:
        response = lambda_client.get_function(FunctionName=LAMBDA_FUNCTION_NAME)
        last_modified_str = response.get("Configuration", {}).get("LastModified", "")
        if last_modified_str:
            # Parse ISO format: "2026-01-06T16:11:36.000+0000"
            # Handle both formats with and without milliseconds
            try:
                last_modified = datetime.fromisoformat(last_modified_str.replace("+0000", "+00:00"))
            except ValueError:
                # Try parsing without timezone offset
                last_modified = datetime.strptime(
                    last_modified_str[:19], "%Y-%m-%dT%H:%M:%S"
                ).replace(tzinfo=timezone.utc)
            return last_modified, None
        return None, "Lambda LastModified not found"
    except ClientError as e:
        return None, f"ERROR getting Lambda info: {e}"


def get_lambda_errors_since_update(profile: str) -> tuple[int, str, Optional[str]]:
    """
    Get Lambda errors since the last update or last hour, whichever is more recent.
    
    This helps assess whether a recent deployment has introduced issues.
    
    Returns:
        Tuple of (error_count, time_description, error_message if any)
    """
    session = get_session(profile, LAMBDA_REGION)
    cloudwatch_client = session.client("cloudwatch")
    
    end_time = datetime.now(timezone.utc)
    one_hour_ago = end_time - timedelta(hours=1)
    
    # Get last modified time
    last_modified, err = get_lambda_last_modified(profile)
    if err:
        return 0, "unknown", err
    
    # Use the more recent of: last_modified or one_hour_ago
    if last_modified and last_modified > one_hour_ago:
        start_time = last_modified
        time_desc = f"since update ({last_modified.strftime('%H:%M:%S')} UTC)"
    else:
        start_time = one_hour_ago
        time_desc = "last hour"
    
    # Calculate period in seconds (must be a multiple of 60 for CloudWatch)
    raw_seconds = int((end_time - start_time).total_seconds())
    # Round up to nearest 60 seconds, minimum 60
    period_seconds = max(60, ((raw_seconds + 59) // 60) * 60)
    
    try:
        response = cloudwatch_client.get_metric_statistics(
            Namespace="AWS/Lambda",
            MetricName="Errors",
            Dimensions=[
                {"Name": "FunctionName", "Value": LAMBDA_FUNCTION_NAME},
            ],
            StartTime=start_time,
            EndTime=end_time,
            Period=period_seconds,
            Statistics=["Sum"],
        )
        
        datapoints = response.get("Datapoints", [])
        if datapoints:
            return int(datapoints[0].get("Sum", 0)), time_desc, None
        return 0, time_desc, None
    except ClientError as e:
        return 0, time_desc, f"ERROR getting Lambda metrics: {e}"


def count_nat_instances_by_region(nat_instances: list[dict]) -> dict[str, int]:
    """Count NAT instances by region."""
    counts = {region: 0 for region in REGIONS}
    for instance in nat_instances:
        region = instance.get("region", "")
        if region in counts:
            counts[region] += 1
    return counts


def get_nat_gateway_stats(profiles: list[str], regions: list[str]) -> dict:
    """
    Get NAT Gateway statistics across all profiles and regions.
    
    Returns:
        Dictionary with:
        - by_tag_value: dict mapping each ci-nat-replace value to its count
        - untagged: count of NAT Gateways with no ci-nat-replace tag
        - by_profile: breakdown by profile
    """
    stats = {
        "by_tag_value": {},  # Maps tag value -> count
        "untagged": 0,
        "by_profile": {},
    }
    
    for profile in profiles:
        profile_stats = {"by_tag_value": {}, "untagged": 0}
        
        for region in regions:
            try:
                session = get_session(profile, region)
                ec2_client = session.client("ec2")
                
                # Get all NAT Gateways that are available (not deleted/pending)
                paginator = ec2_client.get_paginator("describe_nat_gateways")
                for page in paginator.paginate(
                    Filters=[{"Name": "state", "Values": ["available", "pending"]}]
                ):
                    for nat_gw in page.get("NatGateways", []):
                        # Check for ci-nat-replace tag
                        tags = {t["Key"]: t["Value"] for t in nat_gw.get("Tags", [])}
                        
                        if "ci-nat-replace" not in tags:
                            profile_stats["untagged"] += 1
                        else:
                            tag_value = tags["ci-nat-replace"]
                            profile_stats["by_tag_value"][tag_value] = profile_stats["by_tag_value"].get(tag_value, 0) + 1
            except ClientError as e:
                # Log but continue - don't fail the whole check
                pass
        
        # Aggregate into global stats
        for tag_value, count in profile_stats["by_tag_value"].items():
            stats["by_tag_value"][tag_value] = stats["by_tag_value"].get(tag_value, 0) + count
        stats["untagged"] += profile_stats["untagged"]
        stats["by_profile"][profile] = profile_stats
    
    return stats


def check_nat_instance_effectiveness(nat_instances: list[dict]) -> dict:
    """
    Check the effectiveness of NAT instances by verifying route table updates.
    
    For NAT instances older than NAT_INSTANCE_EFFECTIVENESS_MINUTES, checks whether
    the route table has been updated to route traffic through the NAT instance
    instead of a NAT Gateway.
    
    Returns:
        Dictionary with effectiveness statistics and details
    """
    now = datetime.now(timezone.utc)
    min_age = timedelta(minutes=NAT_INSTANCE_EFFECTIVENESS_MINUTES)
    
    # Filter to instances old enough to evaluate
    eligible_instances = [
        inst for inst in nat_instances
        if inst.get("launch_time") and (now - inst["launch_time"]) > min_age
    ]
    
    if not eligible_instances:
        return {
            "eligible_count": 0,
            "effective_count": 0,
            "ineffective_count": 0,
            "percentage": 100.0,
            "ineffective_instances": [],
        }
    
    effective_count = 0
    skipped_count = 0  # Terminated/shutting-down instances are skipped
    ineffective_instances = []
    
    # Group instances by profile and region for efficient API calls
    by_profile_region = {}
    for inst in eligible_instances:
        key = (inst["profile"], inst["region"])
        if key not in by_profile_region:
            by_profile_region[key] = []
        by_profile_region[key].append(inst)
    
    for (profile, region), instances in by_profile_region.items():
        session = get_session(profile, region)
        ec2_client = session.client("ec2")
        
        for inst in instances:
            instance_id = inst["instance_id"]
            vpc_id = inst["vpc_id"]
            
            try:
                # Get the instance state
                instance_response = ec2_client.describe_instances(
                    InstanceIds=[instance_id]
                )
                
                if not instance_response.get("Reservations"):
                    continue
                    
                instance_data = instance_response["Reservations"][0]["Instances"][0]
                instance_state = instance_data.get("State", {}).get("Name", "")
                
                # Skip terminated instances - they restore the NAT gateway route on termination
                if instance_state in ("terminated", "shutting-down"):
                    skipped_count += 1
                    continue
                
                # Get all route tables in the VPC
                route_tables_response = ec2_client.describe_route_tables(
                    Filters=[{"Name": "vpc-id", "Values": [vpc_id]}]
                )
                
                # Check if any route table has a 0.0.0.0/0 route pointing to this instance
                # Note: replace-route with --instance-id sets InstanceId, not NetworkInterfaceId
                found_route = False
                for rt in route_tables_response.get("RouteTables", []):
                    for route in rt.get("Routes", []):
                        if route.get("DestinationCidrBlock") == "0.0.0.0/0":
                            # Check if this route points to our NAT instance
                            if route.get("InstanceId") == instance_id:
                                found_route = True
                                break
                    if found_route:
                        break
                
                if found_route:
                    effective_count += 1
                else:
                    ineffective_instances.append({
                        **inst,
                        "reason": "No route table updated to use this instance",
                    })
                    
            except ClientError as e:
                ineffective_instances.append({
                    **inst,
                    "reason": f"Error checking: {e}",
                })
    
    eligible_count = len(eligible_instances) - skipped_count  # Exclude terminated instances
    ineffective_count = len(ineffective_instances)
    percentage = (effective_count / eligible_count * 100) if eligible_count > 0 else 100.0
    
    return {
        "eligible_count": eligible_count,
        "effective_count": effective_count,
        "ineffective_count": ineffective_count,
        "percentage": percentage,
        "ineffective_instances": ineffective_instances,
    }


def check_expected_resources(profile: str) -> tuple[list[str], list[str]]:
    """
    Verify that all expected resources exist in the account.
    
    Returns:
        Tuple of (missing_resources list, present_resources list)
    """
    missing = []
    present = []
    
    session = get_session(profile, LAMBDA_REGION)
    
    # Check Lambda function
    lambda_client = session.client("lambda")
    try:
        lambda_client.get_function(FunctionName=LAMBDA_FUNCTION_NAME)
        present.append(f"Lambda: {LAMBDA_FUNCTION_NAME}")
    except ClientError as e:
        if "ResourceNotFoundException" in str(e):
            missing.append(f"MISSING Lambda: {LAMBDA_FUNCTION_NAME}")
        else:
            missing.append(f"ERROR checking Lambda: {e}")
    
    # Check IAM instance profile
    iam_client = session.client("iam")
    try:
        iam_client.get_instance_profile(InstanceProfileName=EXPECTED_INSTANCE_PROFILE)
        present.append(f"Instance Profile: {EXPECTED_INSTANCE_PROFILE}")
    except ClientError as e:
        if "NoSuchEntity" in str(e):
            missing.append(f"MISSING Instance Profile: {EXPECTED_INSTANCE_PROFILE}")
        else:
            missing.append(f"ERROR checking Instance Profile: {e}")
    
    # Check IAM roles
    for role_name in EXPECTED_ROLES:
        try:
            iam_client.get_role(RoleName=role_name)
            present.append(f"Role: {role_name}")
        except ClientError as e:
            if "NoSuchEntity" in str(e):
                missing.append(f"MISSING Role: {role_name}")
            else:
                missing.append(f"ERROR checking Role {role_name}: {e}")
    
    # Check EventBridge rule in us-east-1
    events_client = session.client("events")
    try:
        events_client.describe_rule(Name=EXPECTED_EVENTBRIDGE_RULE)
        present.append(f"EventBridge Rule: {EXPECTED_EVENTBRIDGE_RULE} (us-east-1)")
    except ClientError as e:
        if "ResourceNotFoundException" in str(e):
            missing.append(f"MISSING EventBridge Rule: {EXPECTED_EVENTBRIDGE_RULE} (us-east-1)")
        else:
            missing.append(f"ERROR checking EventBridge Rule: {e}")
    
    # Check forwarder rules in other regions
    for region in FORWARDER_REGIONS:
        region_session = get_session(profile, region)
        region_events_client = region_session.client("events")
        try:
            region_events_client.describe_rule(Name=EXPECTED_FORWARDER_RULE)
            present.append(f"Forwarder Rule: {EXPECTED_FORWARDER_RULE} ({region})")
        except ClientError as e:
            if "ResourceNotFoundException" in str(e):
                missing.append(f"MISSING Forwarder Rule: {EXPECTED_FORWARDER_RULE} ({region})")
            else:
                missing.append(f"ERROR checking Forwarder Rule ({region}): {e}")
    
    return missing, present


def run_check(play_alarm_on_issues: bool = False) -> bool:
    """
    Run all monitoring checks.
    
    Args:
        play_alarm_on_issues: If True, play an alarm sound when issues are detected.
    
    Returns:
        True if problems were found, False otherwise.
    """
    timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    all_issues = []
    all_nat_instances = []
    has_problems = False
    
    print("=" * 60)
    print(f"Resource Monitor Check: {timestamp}")
    print("=" * 60)
    print()
    
    # Collect data from all profiles
    for profile in PROFILES:
        print(f"{YELLOW}Checking profile: {profile}{NC}")
        profile_issues = []
        
        # Check expected resources exist
        missing_resources, present_resources = check_expected_resources(profile)
        if missing_resources:
            profile_issues.extend([f"  {r}" for r in missing_resources])
        
        # Check instance profile count
        ip_count, ip_warning = check_instance_profile_count(profile)
        if ip_warning:
            profile_issues.append(ip_warning)
        
        # Check for orphaned IAM resources
        iam_issues = check_orphaned_iam_resources(profile)
        profile_issues.extend(iam_issues)
        
        # Check each region for orphaned EC2 resources
        for region in REGIONS:
            ec2_issues, nat_instances = check_orphaned_ec2_resources(profile, region)
            all_nat_instances.extend(nat_instances)
            if ec2_issues:
                profile_issues.append(f"  Region {region}:")
                profile_issues.extend([f"  {issue}" for issue in ec2_issues])
        
        # Get Lambda error count (only for us-east-1 where Lambda runs)
        error_count, error_msg = get_lambda_error_count(profile)
        if error_msg:
            profile_issues.append(f"  {error_msg}")
        elif error_count > 0:
            profile_issues.append(f"  LAMBDA ERRORS: {error_count} errors in the last {NAT_INSTANCE_AGE_THRESHOLD_HOURS} hours")
        
        if profile_issues:
            print(f"{RED}ISSUES FOUND:{NC}")
            for issue in profile_issues:
                print(issue)
            all_issues.extend([f"Profile {profile}:"] + profile_issues)
            has_problems = True
        else:
            print(f"{GREEN}  No issues found{NC}")
        print()
    
    # Check NAT instance age across all profiles
    age_issues = check_nat_instance_age(all_nat_instances)
    if age_issues:
        print(f"{RED}NAT INSTANCE AGE ISSUES:{NC}")
        for issue in age_issues:
            print(issue)
        all_issues.extend(["NAT Instance Age Issues:"] + age_issues)
        has_problems = True
        print()
    
    # Display NAT instance counts by region
    print(f"{CYAN}NAT Instance Counts by Region:{NC}")
    region_counts = count_nat_instances_by_region(all_nat_instances)
    total_instances = 0
    for region in REGIONS:
        count = region_counts[region]
        total_instances += count
        print(f"  {region}: {count}")
    print(f"  Total: {total_instances}")
    print()
    
    # Display NAT Gateway replacement stats
    print(f"{CYAN}NAT Gateway Replacement Status:{NC}")
    nat_gw_stats = get_nat_gateway_stats(PROFILES, REGIONS)
    by_tag_value = nat_gw_stats["by_tag_value"]
    untagged = nat_gw_stats["untagged"]
    total_tagged = sum(by_tag_value.values())
    total_nat_gw = total_tagged + untagged
    
    # Sort tag values: "true" first, then alphabetically
    sorted_values = sorted(by_tag_value.keys(), key=lambda x: (x.lower() != "true", x.lower()))
    for tag_value in sorted_values:
        count = by_tag_value[tag_value]
        print(f"  NAT Gateways with ci-nat-replace={tag_value}: {count}")
    print(f"  NAT Gateways with no ci-nat-replace tag: {untagged}")
    print(f"  Total NAT Gateways: {total_nat_gw}")
    print(f"  NAT Instances launched: {total_instances}")
    
    # Only "true" results in replacement
    true_count = by_tag_value.get("true", 0)
    if true_count > 0:
        ratio = total_instances / true_count
        print(f"  NAT Instance to Replaced Gateway ratio: {ratio:.2f}")
    if total_nat_gw > 0:
        all_ratio = total_instances / total_nat_gw
        print(f"  NAT Instance to ALL Gateway ratio: {all_ratio:.2f}")
    print()
    
    # Check NAT instance effectiveness
    print(f"{CYAN}NAT Instance Effectiveness (instances > {NAT_INSTANCE_EFFECTIVENESS_MINUTES} min old):{NC}")
    effectiveness = check_nat_instance_effectiveness(all_nat_instances)
    if effectiveness["eligible_count"] == 0:
        print(f"  No NAT instances old enough to evaluate")
    else:
        pct = effectiveness["percentage"]
        if pct >= 95:
            color = GREEN
        elif pct >= 80:
            color = YELLOW
        else:
            color = RED
        print(f"  Eligible instances: {effectiveness['eligible_count']}")
        print(f"  Effective (route updated): {effectiveness['effective_count']}")
        print(f"  Ineffective: {effectiveness['ineffective_count']}")
        print(f"  Effectiveness: {color}{pct:.1f}%{NC}")
        
        # Report ineffective instances as issues if any
        if effectiveness["ineffective_instances"]:
            now = datetime.now(timezone.utc)
            print(f"\n  {RED}Ineffective NAT instances:{NC}")
            for inst in effectiveness["ineffective_instances"]:
                age_str = ""
                if inst.get("launch_time"):
                    age_str = f" [{format_duration(now - inst['launch_time'])}]"
                print(f"    - {inst['instance_id']} ({inst['instance_name']}) in {inst['profile']}/{inst['region']}{age_str}")
                print(f"      Reason: {inst['reason']}")
            # Add to issues
            all_issues.append("NAT Instance Effectiveness Issues:")
            for inst in effectiveness["ineffective_instances"]:
                age_str = ""
                if inst.get("launch_time"):
                    age_str = f" [{format_duration(now - inst['launch_time'])}]"
                all_issues.append(
                    f"  INEFFECTIVE: {inst['instance_id']} ({inst['instance_name']}) "
                    f"in {inst['profile']}/{inst['region']}{age_str} - {inst['reason']}"
                )
            has_problems = True
    print()
    
    # Display Lambda error summary
    print(f"{CYAN}Lambda Error Summary (last {NAT_INSTANCE_AGE_THRESHOLD_HOURS} hours):{NC}")
    total_errors = 0
    for profile in PROFILES:
        error_count, _ = get_lambda_error_count(profile)
        total_errors += error_count
        if error_count > 0:
            print(f"  {profile}: {RED}{error_count} errors{NC}")
        else:
            print(f"  {profile}: {GREEN}0 errors{NC}")
    print(f"  Total: {total_errors}")
    print()
    
    # Display errors since last update (or last hour)
    print(f"{CYAN}Lambda Errors Since Update (or last hour):{NC}")
    total_recent_errors = 0
    for profile in PROFILES:
        error_count, time_desc, err = get_lambda_errors_since_update(profile)
        total_recent_errors += error_count
        if err:
            print(f"  {profile}: {YELLOW}{err}{NC}")
        elif error_count > 0:
            print(f"  {profile}: {RED}{error_count} errors ({time_desc}){NC}")
        else:
            print(f"  {profile}: {GREEN}0 errors ({time_desc}){NC}")
    print(f"  Total: {total_recent_errors}")
    print()
    
    # Display infrastructure health summary
    print(f"{CYAN}Infrastructure Health:{NC}")
    all_healthy = True
    for profile in PROFILES:
        missing, present = check_expected_resources(profile)
        if missing:
            print(f"  {profile}: {RED}UNHEALTHY - {len(missing)} missing resource(s){NC}")
            all_healthy = False
        else:
            print(f"  {profile}: {GREEN}OK ({len(present)} resources){NC}")
    print()
    
    if has_problems:
        print(f"{RED}{'=' * 60}")
        print("ALERT: Resource issues detected!")
        print(f"{'=' * 60}{NC}")
        print()
        for issue in all_issues:
            print(issue)
        if play_alarm_on_issues:
            play_alarm()
        return True
    else:
        print(f"{GREEN}All checks passed - no issues detected{NC}")
        return False


def main():
    parser = argparse.ArgumentParser(
        description="Monitor NAT instance resources for leaks and issues."
    )
    parser.add_argument(
        "-o", "--once",
        action="store_true",
        help="Run once and exit (don't loop)",
    )
    parser.add_argument(
        "-i", "--interval",
        type=int,
        default=CHECK_INTERVAL_SECONDS,
        help=f"Check interval in seconds (default: {CHECK_INTERVAL_SECONDS})",
    )
    parser.add_argument(
        "-a", "--alarm",
        action="store_true",
        help="Play an alarm sound when issues are detected",
    )
    args = parser.parse_args()
    
    print("NAT Instance Resource Monitor")
    print(f"Checking profiles: {', '.join(PROFILES)}")
    print(f"Checking regions: {', '.join(REGIONS)}")
    print(f"Instance profile threshold: {INSTANCE_PROFILE_THRESHOLD}")
    print(f"NAT instance age threshold: {NAT_INSTANCE_AGE_THRESHOLD_HOURS} hours")
    print(f"Check interval: {args.interval}s")
    print()
    
    if args.once:
        has_problems = run_check(play_alarm_on_issues=args.alarm)
        sys.exit(1 if has_problems else 0)
    else:
        while True:
            try:
                run_check(play_alarm_on_issues=args.alarm)
                print()
                print(f"Next check in {args.interval} seconds... (Ctrl+C to stop)")
                time.sleep(args.interval)
            except KeyboardInterrupt:
                print("\nMonitoring stopped.")
                break


if __name__ == "__main__":
    main()

