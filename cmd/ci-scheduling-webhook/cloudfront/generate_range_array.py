#!/usr/bin/env python3

# THIS IS NOT PART OF THE LAMBDA EXECUTION. IT IS USED TO GENERATE DATA FOR
# THE LAMBDA. COPY OUTPUT ARRAY INTO lambda_function.py.

# Outputs an array[[ip_start, ip_stop], [ip_start, ip_stop], ...] for
# all EC2 CIDR ranges in us-east. This is used to inform our CloudFront
# function and should be pasted directly into its code.

# For efficient data transfers, we need EC2 instances within the us-east
# region to use their VPC S3 gateway endpoints to access data on S3.
# Transfer costs intra region like this are free.
# According to https://aws.amazon.com/premiumsupport/knowledge-center/vpc-endpoints-cross-region-aws-services/
# traffic to buckets in other Regions will travel over the internet.
# We want all registry access from AWS us-east, to use VPC gateway endpoints
# while other reads go through CloudFront. Our CloudFront data transfer
# is discounted. It is much less expensive tan S3 traffic to the Internet (averaging about >$0.05).
#
# The goal of this script is to help our CloudFront viewer request function, written in nodejs
# rapidly determine whether an incoming request is from EC2 & us-east. If it is, the function
# will redirect the client back to S3. If it is not, the request will be fulfilled through
# CloudFront.
#
# The AWS IP ranges will change periodically. Our redirections do not have to be 100% accurate.
# both CloudFront and S3 will resolve. We just need the statistically maajority of us-east
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

ranges_to_reroute = []
for cb in cidrs:

    service = cb['service']
    if service.lower() != "ec2":
        continue

    region = cb['region']
    if not region.startswith('us-east'):
        # transfers outside of us-east should be done with
        # cloudfront. Our discount makes GBs = 0.0174 whereas
        # transfers to us-west are 0.02. Transfers between
        # us-east regions are free or 0.01.
        continue

    prefix = cb['ip_prefix']
    net = ip_network(prefix)

    first_address_decimal = int(ip_address(net[0]))
    last_address_decimal = int(ip_address(net[-1]))
    ranges_to_reroute.append([first_address_decimal, last_address_decimal])

print(f'Found {len(ranges_to_reroute)} qualifying ranges')

print(json.dumps(ranges_to_reroute))
