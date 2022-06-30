#!/usr/bin/env python3

from typing import List, Dict, Tuple

# THIS IS NOT PART OF THE LAMBDA EXECUTION. IT IS USED TO GENERATE DATA FOR
# THE LAMBDA. COPY OUTPUT ARRAY INTO lambda_function.py.

# Outputs a dict of region-name to array[[ip_start, ip_stop], [ip_start, ip_stop], ...] for
# all EC2 CIDR ranges in the US. This is used to inform our CloudFront
# function and should be pasted directly into its code.

# In short, we want:
# 1. all us-east-1 AWS based registry access to use VPC gateway endpoints  (free)
# 2. all non us-east-1 AWS based registries to use their regionally replicated registry (free + replication costs)
# 3. all other traffic to go through CloudFront (our discount + NAT costs on client side)

# Understanding #1
# OpenShift's vanilla install in AWS includes a VPC gateway endpoint
# for S3 which allows hosts within a private subnet (all OCP instances)
# to communicate to S3 at zero cost to an S3 bucket when it is in the same region. If
# an instance does not use this endpoint, traffic is processed through
# the NAT -- which is very expensive. Our build farm S3 registry buckets are in
# us-east-1. So for all us-east-1 IP addresses, redirect the caller to use
# the S3 hostname -- ensuring traffic goes through the inexpensive S3 VPC endpoint.
#
# Understanding #2
# When an EC2 instance in another region and a private subnet makes an S3 request, the
# CloudFront lambda will determine which region the caller is from and redirect them,
# if possible, to a regional replication of their S3 bucket.
#
# Understanding #3
# For non-us-east-1 and non-us-east-2, go through CloudFront. This eliminates S3 data transfer
# costs, but keeps the NAT processing cost on the client side if it is in
# AWS. For non-AWS clusters, always go through CloudFront. S3 transferring
# out to the Internet ranges between 0.09 and 0.05 per GB -- our CloudFront
# is less expensive than this.
#
#
# The goal of this script is to help our CloudFront viewer request function (lambda_function.py)
# rapidly determine whether an incoming request is from a specific EC2 region. If it is, the function
# will redirect the client back to S3. If it is not, the request will be fulfilled through
# CloudFront.
#
# The AWS IP ranges will change periodically. Our redirections do not have to be 100% accurate.
# both CloudFront and S3 will resolve. We just need the statistically majority of AWS
# requests to go through S3.
#
# It may be worth re-running this script once in awhile to refresh the IP ranges in the
# nodejs CloudFront Viewer request Lambda@Edge function.

import urllib.request
from ipaddress import ip_network, ip_address
import json

AWS_IP_RANGES_URL = "https://ip-ranges.amazonaws.com/ip-ranges.json"

with urllib.request.urlopen(AWS_IP_RANGES_URL) as f:
    cidrs = json.load(f)['prefixes']

ranges_by_region: Dict[str, List[Tuple[int, int]]] = {}
for cb in cidrs:

    service = cb['service']
    if service.lower() != "ec2":
        continue

    region = cb['region']
    if not region.startswith('us-'):
        continue

    if region not in ranges_by_region:
        region_range: List[Tuple[int, int]] = []
        ranges_by_region[region] = region_range
    else:
        region_range = ranges_by_region[region]

    prefix = cb['ip_prefix']
    net = ip_network(prefix)

    first_address_decimal = int(ip_address(net[0]))
    last_address_decimal = int(ip_address(net[-1]))
    region_range.append((first_address_decimal, last_address_decimal))


for region_range in ranges_by_region.values():
    # Sort from low to high in order to allow an optimized search in lambda_function.py
    region_range.sort(key=lambda entry: entry[0])

print(json.dumps(ranges_by_region))
