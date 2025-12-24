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


def count_nat_instances_by_region(nat_instances: list[dict]) -> dict[str, int]:
    """Count NAT instances by region."""
    counts = {region: 0 for region in REGIONS}
    for instance in nat_instances:
        region = instance.get("region", "")
        if region in counts:
            counts[region] += 1
    return counts


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

